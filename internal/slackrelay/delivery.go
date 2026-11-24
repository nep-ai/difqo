package slackrelay

import (
	"context"
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/nep-ai/difqo/internal/slackintake"
)

var deliveryID = regexp.MustCompile(`^[A-Za-z0-9]{2,64}$`)

func (r *relay) deliveries(w http.ResponseWriter, req *http.Request) {
	id, proof, ok := pairingAuth(req)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	batch, err := r.pairings.deliveries(req.Context(), id, proof)
	if err != nil {
		pairingError(w, err)
		return
	}
	writeJSON(w, batch)
}

func (r *relay) receipt(w http.ResponseWriter, req *http.Request) {
	id, proof, ok := pairingAuth(req)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	eventID := req.PathValue("event")
	if !deliveryID.MatchString(eventID) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := r.pairings.receipt(req.Context(), id, proof, eventID); err != nil {
		pairingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s pairingStore) deliveries(ctx context.Context, id, proof string) (slackintake.DeliveryBatch, error) {
	batch := slackintake.DeliveryBatch{Events: []slackintake.Delivery{}}
	err := s.owned(ctx, id, proof, false, func(tx pgx.Tx, p pairing) error {
		if !p.claimed || p.terminal() {
			return errPairingState
		}
		batch.Binding = s.binding(p)
		rows, err := tx.Query(ctx, `
			SELECT event_id, goal FROM slack_relay_events
			WHERE app_id = $1 AND workspace_id = $2 AND pairing_id = $3 AND goal IS NOT NULL
			ORDER BY queued_at, event_id LIMIT $4
		`, s.appID, p.workspace, id, slackintake.DeliveryLimit)
		if err != nil {
			return err
		}
		batch.Events, err = pgx.AppendRows(batch.Events, rows, pgx.RowToStructByPos[slackintake.Delivery])
		return err
	})
	return batch, err
}

func (s pairingStore) receipt(ctx context.Context, id, proof, eventID string) error {
	return s.owned(ctx, id, proof, false, func(tx pgx.Tx, p pairing) error {
		if !p.claimed || p.terminal() {
			return errPairingState
		}
		tag, err := tx.Exec(ctx, `
			UPDATE slack_relay_events SET goal = NULL
			WHERE app_id = $1 AND workspace_id = $2 AND pairing_id = $3 AND event_id = $4
		`, s.appID, p.workspace, id, eventID)
		if err == nil && tag.RowsAffected() == 0 {
			return errPairingState
		}
		return err
	})
}
