package slackintake

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/nep-ai/difqo/internal/store"
)

var ErrRelayUnavailable = errors.New("Slack relay is unavailable or returned an invalid result")

type Connections struct {
	tasks           store.Tasks
	origin          string
	client          *http.Client
	enrollmentToken string
}

type ConnectionView struct {
	Mode       string              `json:"mode"`
	Generation int64               `json:"generation"`
	State      string              `json:"state"`
	OAuthURL   string              `json:"oauth_url,omitempty"`
	PairingID  string              `json:"pairing_id,omitempty"`
	Workspace  string              `json:"verified_workspace,omitempty"`
	Operator   string              `json:"verified_operator,omitempty"`
	Installed  string              `json:"installed_workspace,omitempty"`
	Binding    *store.SlackBinding `json:"binding,omitempty"`
}

func connectionView(s store.SlackConnection) ConnectionView {
	v := ConnectionView{Mode: s.Mode, Generation: s.Generation, State: "disconnected"}
	if s.Mode == "private" && s.Private != nil {
		v.State, v.Binding = "connected", s.Private
	}
	if p := s.Pairing; p != nil {
		v.State, v.PairingID, v.Workspace, v.Operator, v.Installed = p.State, p.ID, p.Workspace, p.Operator, p.Installed
		if v.State == "ready_to_link" || s.Mode == "official" {
			v.Binding = &p.Binding
		}
		if s.Mode != "official" && !transitionPending(p.State) && p.State != "reconnect_required" && !time.Now().Before(p.Expires) {
			v.State, v.Binding = "expired", nil
		}
	}
	return v
}

func transitionPending(state string) bool {
	return state == "claiming" || state == "claim_rejected" || state == "disconnecting"
}

func (c *Connections) Status(ctx context.Context) (ConnectionView, error) {
	s, err := c.tasks.SlackConnection(ctx)
	return connectionView(s), err
}

func (c *Connections) Begin(ctx context.Context) (ConnectionView, error) {
	s, err := c.tasks.UpdateSlackConnection(ctx, func(s *store.SlackConnection) error {
		if s.Mode == "official" || (s.Mode == "private" && s.Private != nil) {
			return store.ErrSlackTransition
		}
		if s.Pairing != nil && transitionPending(s.Pairing.State) {
			return store.ErrSlackTransition
		}
		if s.Pairing != nil && s.Pairing.Relay == c.origin && s.Pairing.State != "reconnect_required" &&
			time.Now().Before(s.Pairing.Expires) {
			return nil
		}
		var id [16]byte
		var proof [32]byte
		rand.Read(id[:])
		rand.Read(proof[:])
		if s.Mode != "disconnected" {
			s.Generation++
		}
		s.Mode = "disconnected"
		s.Pairing = &store.SlackPairing{ID: hex.EncodeToString(id[:]), Proof: hex.EncodeToString(proof[:]),
			Relay: c.origin, State: "connecting", Expires: time.Now().UTC().Add(15 * time.Minute)}
		return nil
	})
	if err == nil && c.enrollmentToken != "" {
		err = c.enroll(ctx, s.Pairing)
	}
	return connectionView(s), err
}

type relayStatus struct {
	State     string             `json:"state"`
	Workspace string             `json:"verified_workspace"`
	Operator  string             `json:"verified_operator"`
	Installed string             `json:"installed_workspace"`
	Binding   store.SlackBinding `json:"binding"`
}

func (c *Connections) relayStatus(ctx context.Context, p *store.SlackPairing) (relayStatus, error) {
	var status relayStatus
	res, err := c.relayRequest(ctx, http.MethodPut, p, "", nil)
	if err != nil {
		return status, ErrRelayUnavailable
	}
	defer res.Body.Close()
	limited := &io.LimitedReader{R: res.Body, N: maxEventSize + 1}
	decoder := json.NewDecoder(limited)
	if res.StatusCode != http.StatusOK || decoder.Decode(&status) != nil ||
		decoder.Decode(new(any)) != io.EOF || limited.N == 0 {
		return status, ErrRelayUnavailable
	}
	switch status.State {
	case "connecting", "approval_needed", "reconnect_required":
	case "installed":
		if !idPattern.MatchString(status.Installed) {
			return status, ErrRelayUnavailable
		}
	case "ready_to_link":
		if validateBinding(status.Binding) != nil || status.Workspace != status.Installed ||
			status.Workspace != status.Binding.WorkspaceID || status.Operator != status.Binding.OperatorID {
			return status, ErrRelayUnavailable
		}
	default:
		return status, ErrRelayUnavailable
	}
	workspace := status.Workspace
	if workspace == "" {
		workspace = p.Workspace
	}
	if (status.Workspace == "") != (status.Operator == "") ||
		(status.Workspace != "" && (!idPattern.MatchString(status.Workspace) || !idPattern.MatchString(status.Operator))) ||
		(status.Installed != "" && !idPattern.MatchString(status.Installed)) ||
		(workspace != "" && status.Installed != "" && workspace != status.Installed) ||
		(p.Workspace != "" && status.Workspace != "" && (p.Workspace != status.Workspace || p.Operator != status.Operator)) {
		return status, ErrRelayUnavailable
	}
	return status, nil
}

func (c *Connections) Refresh(ctx context.Context, id string) (ConnectionView, error) {
	before, err := c.tasks.SlackConnection(ctx)
	if err != nil {
		return ConnectionView{}, err
	}
	p := before.Pairing
	if p == nil || p.ID != id || p.Relay != c.origin || p.State == "reconnect_required" || transitionPending(p.State) ||
		(before.Mode != "official" && !time.Now().Before(p.Expires)) {
		return connectionView(before), store.ErrSlackTransition
	}
	status, relayErr := c.relayStatus(ctx, p)
	after, err := c.tasks.UpdateSlackConnection(ctx, func(s *store.SlackConnection) error {
		if s.Generation != before.Generation || s.Pairing == nil || *s.Pairing != *p ||
			(s.Mode != "official" && !time.Now().Before(p.Expires)) {
			return store.ErrSlackTransition
		}
		if relayErr != nil {
			if s.Mode == "official" {
				s.Pairing.State = "reconnecting"
			}
			return nil
		}
		if status.State == "reconnect_required" {
			requireReconnect(s)
			return nil
		}
		if s.Mode == "official" {
			if status.State != "ready_to_link" || status.Binding != p.Binding {
				return store.ErrSlackTransition
			}
			s.Pairing.State = "connected"
			return nil
		}
		s.Pairing.State = status.State
		if status.Workspace != "" {
			s.Pairing.Workspace, s.Pairing.Operator = status.Workspace, status.Operator
		}
		s.Pairing.Installed, s.Pairing.Binding = status.Installed, status.Binding
		return nil
	})
	if err != nil {
		return connectionView(after), err
	}
	return connectionView(after), relayErr
}

func (c *Connections) Confirm(ctx context.Context, id string, binding store.SlackBinding) (ConnectionView, error) {
	state, err := c.tasks.UpdateSlackConnection(ctx, func(s *store.SlackConnection) error {
		p := s.Pairing
		if p == nil || p.ID != id || p.Relay != c.origin || p.Binding != binding {
			return store.ErrSlackTransition
		}
		if s.Mode == "official" || p.State == "claiming" {
			return nil
		}
		if s.Mode != "disconnected" || p.State != "ready_to_link" || !time.Now().Before(p.Expires) {
			return store.ErrSlackTransition
		}
		p.State = "claiming"
		return nil
	})
	if err != nil || state.Pairing.RelayClaimed {
		return connectionView(state), err
	}
	return c.finishClaim(ctx, state)
}

func (c *Connections) Disconnect(ctx context.Context, generation int64, id string) (ConnectionView, error) {
	state, err := c.tasks.UpdateSlackConnection(ctx, func(s *store.SlackConnection) error {
		p := s.Pairing
		if s.Generation != generation || (p != nil && p.ID != id) || (p == nil && id != "") {
			return store.ErrSlackTransition
		}
		if p != nil && p.State == "disconnecting" {
			return nil
		}
		if p != nil && p.Proof != "" && p.Relay != c.origin {
			return store.ErrSlackTransition
		}
		s.Mode = "disconnected"
		s.Generation++
		if p == nil || p.Proof == "" {
			s.Pairing = nil
		} else {
			p.State = "disconnecting"
		}
		return nil
	})
	if err != nil || state.Pairing == nil {
		return connectionView(state), err
	}
	return c.finishDisconnect(ctx, state)
}

func (c *Connections) EnablePrivate(ctx context.Context, generation int64) (ConnectionView, error) {
	s, err := c.tasks.UpdateSlackConnection(ctx, func(s *store.SlackConnection) error {
		if s.Mode != "disconnected" || s.Generation != generation || s.Pairing != nil {
			return store.ErrSlackTransition
		}
		s.Mode = "private"
		s.Generation++
		return nil
	})
	return connectionView(s), err
}
