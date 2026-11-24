package slackrelay

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nep-ai/difqo/internal/store"
)

func (s pairingStore) claim(ctx context.Context, id, proof string, expected store.SlackBinding) error {
	err := s.owned(ctx, id, proof, false, func(tx pgx.Tx, p pairing) error {
		if p.terminal() || p.channel == "" || s.binding(p) != expected {
			return errPairingState
		}
		if p.claimed {
			return nil
		}
		tag, err := tx.Exec(ctx, `
			UPDATE slack_relay_pairings SET claimed = true
			WHERE app_id = $1 AND id = $2 AND expires_at > clock_timestamp()
		`, s.appID, id)
		if err == nil && tag.RowsAffected() == 0 {
			return errPairingState
		}
		return err
	})
	var constraint *pgconn.PgError
	if errors.As(err, &constraint) && constraint.ConstraintName == "slack_relay_one_active_dm" {
		return errPairingState
	}
	return err
}

func (s pairingStore) binding(p pairing) store.SlackBinding {
	return store.SlackBinding{AppID: s.appID, WorkspaceID: p.identity.Workspace,
		OperatorID: p.identity.Operator, ChannelID: p.channel}
}

func (s pairingStore) enqueue(ctx context.Context, binding store.SlackBinding, eventID, goal string) error {
	if binding.AppID != s.appID {
		return nil
	}
	// Lock the exact claimed pairing in the insertion statement. Revocation
	// either wins first or clears the queued goal in its own transaction.
	_, err := s.db.Exec(ctx, `
		INSERT INTO slack_relay_events (app_id, workspace_id, event_id, pairing_id, goal)
		SELECT app_id, verified_workspace, $5, id, $6
		FROM slack_relay_pairings
		WHERE app_id = $1 AND verified_workspace = $2 AND verified_operator = $3 AND dm_channel = $4 AND claimed AND pilot_scope = $7
		FOR UPDATE
		ON CONFLICT (app_id, workspace_id, event_id) DO NOTHING
	`, s.appID, binding.WorkspaceID, binding.OperatorID, binding.ChannelID, eventID, goal, s.scope)
	return err
}
