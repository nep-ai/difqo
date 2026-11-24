package slackintake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/nep-ai/difqo/internal/store"
)

const DeliveryLimit = 8

type Delivery struct {
	EventID string `json:"event_id"`
	Goal    string `json:"goal"`
}

type DeliveryBatch struct {
	Binding store.SlackBinding `json:"binding"`
	Events  []Delivery         `json:"events"`
}

func (c *Connections) relayRequest(ctx context.Context, method string, p *store.SlackPairing, suffix string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.origin+"/pairings/"+p.ID+suffix, body)
	if err != nil {
		return nil, ErrRelayUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+p.Proof)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client.Do(req)
	if err != nil {
		return nil, ErrRelayUnavailable
	}
	return res, nil
}

func (c *Connections) relayChange(ctx context.Context, method string, p *store.SlackPairing, suffix string, body io.Reader) error {
	res, err := c.relayRequest(ctx, method, p, suffix, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		return store.ErrSlackTransition
	default:
		return ErrRelayUnavailable
	}
}

// Run polls the loopback fixture while the API server is running.
func (c *Connections) Run(ctx context.Context) {
	for delay := time.Second; ctx.Err() == nil; {
		err := c.sync(ctx)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err == nil {
			delay = time.Second
		} else {
			delay = min(delay*2, 15*time.Second)
		}
	}
}

func (c *Connections) sync(ctx context.Context) error {
	state, err := c.tasks.SlackConnection(ctx)
	if err != nil || state.Pairing == nil || state.Pairing.Relay != c.origin {
		return err
	}
	switch state.Pairing.State {
	case "disconnecting":
		_, err := c.finishDisconnect(ctx, state)
		return err
	case "claiming":
		_, err := c.finishClaim(ctx, state)
		return err
	}
	if state.Mode != "official" {
		return nil
	}
	if !state.Pairing.RelayClaimed {
		_, err := c.finishClaim(ctx, state)
		return err
	}
	return c.receive(ctx, state)
}

func (c *Connections) receive(ctx context.Context, state store.SlackConnection) error {
	p := state.Pairing
	res, err := c.relayRequest(ctx, http.MethodGet, p, "/events", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusConflict {
		_, err := c.tasks.UpdateSlackConnection(ctx, func(current *store.SlackConnection) error {
			if !samePairing(current, state) {
				return store.ErrSlackTransition
			}
			requireReconnect(current)
			return nil
		})
		return err
	}
	var batch DeliveryBatch
	limited := &io.LimitedReader{R: res.Body, N: 256<<10 + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if res.StatusCode != http.StatusOK || decoder.Decode(&batch) != nil ||
		decoder.Decode(new(any)) != io.EOF || limited.N == 0 ||
		batch.Binding != p.Binding || len(batch.Events) > DeliveryLimit {
		return ErrRelayUnavailable
	}
	for _, event := range batch.Events {
		if !eventIDPattern.MatchString(event.EventID) || !validGoal(event.Goal) {
			return ErrRelayUnavailable
		}
	}
	for _, event := range batch.Events {
		_, err := c.tasks.ImportOfficialSlack(ctx, state.Generation, p.ID, p.Binding, event.EventID, event.Goal)
		if err != nil {
			return err
		}
		if err := c.relayChange(ctx, http.MethodPut, p, "/events/"+event.EventID, nil); err != nil {
			return err
		}
	}
	return nil
}

func samePairing(current *store.SlackConnection, expected store.SlackConnection) bool {
	return current.Generation == expected.Generation && current.Mode == expected.Mode &&
		current.Pairing != nil && expected.Pairing != nil && *current.Pairing == *expected.Pairing
}

func requireReconnect(s *store.SlackConnection) {
	s.Mode = "disconnected"
	s.Generation++
	s.Pairing = &store.SlackPairing{ID: s.Pairing.ID, State: "reconnect_required"}
}

func (c *Connections) finishClaim(ctx context.Context, expected store.SlackConnection) (ConnectionView, error) {
	p := expected.Pairing
	raw, err := json.Marshal(p.Binding)
	if err != nil {
		return connectionView(expected), err
	}
	relayErr := c.relayChange(ctx, http.MethodPost, p, "/claim", bytes.NewReader(raw))
	if relayErr != nil && !errors.Is(relayErr, store.ErrSlackTransition) {
		return connectionView(expected), relayErr
	}
	state, err := c.tasks.UpdateSlackConnection(ctx, func(s *store.SlackConnection) error {
		if !samePairing(s, expected) {
			if s.Mode == "official" && s.Pairing != nil && s.Pairing.ID == p.ID &&
				s.Pairing.Binding == p.Binding && s.Pairing.RelayClaimed && relayErr == nil {
				return nil
			}
			return store.ErrSlackTransition
		}
		if relayErr != nil {
			// A rejected claim can be cancelled using the same proof. Preserve
			// it until the relay confirms that the pairing is retired.
			if s.Mode == "official" {
				s.Mode = "disconnected"
				s.Generation++
			}
			s.Pairing.State = "claim_rejected"
			return nil
		}
		if s.Mode != "official" {
			s.Generation++
		}
		s.Mode, s.Pairing.State, s.Pairing.RelayClaimed = "official", "connected", true
		return nil
	})
	if err != nil {
		return connectionView(state), err
	}
	return connectionView(state), relayErr
}

func (c *Connections) finishDisconnect(ctx context.Context, expected store.SlackConnection) (ConnectionView, error) {
	if err := c.relayChange(ctx, http.MethodDelete, expected.Pairing, "", nil); err != nil {
		return connectionView(expected), err
	}
	state, err := c.tasks.UpdateSlackConnection(ctx, func(s *store.SlackConnection) error {
		if s.Generation == expected.Generation && s.Pairing == nil {
			return nil
		}
		if !samePairing(s, expected) {
			return store.ErrSlackTransition
		}
		s.Pairing = nil
		return nil
	})
	return connectionView(state), err
}
