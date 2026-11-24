package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrSlackBinding = errors.New("Slack intake is already bound to a different app, workspace, operator, or DM")
var ErrSlackReplay = errors.New("Slack event ID already belongs to a different goal")

type SlackBinding struct {
	AppID       string `json:"app_id"`
	WorkspaceID string `json:"workspace_id"`
	OperatorID  string `json:"operator_id"`
	ChannelID   string `json:"channel_id"`
}

const slackBindingQuery = `
	SELECT app_id, workspace_id, operator_id, channel_id
	FROM slack_intake_binding WHERE singleton`

func readSlackBinding(row *sql.Row, binding SlackBinding) error {
	var saved SlackBinding
	if err := row.Scan(&saved.AppID, &saved.WorkspaceID, &saved.OperatorID, &saved.ChannelID); err != nil {
		return err
	}
	if saved != binding {
		return ErrSlackBinding
	}
	return nil
}

func (s Tasks) CheckSlackBinding(ctx context.Context, binding SlackBinding) (int64, error) {
	state, err := s.SlackConnection(ctx)
	if err != nil {
		return 0, err
	}
	if state.Mode != "private" || (state.Private != nil && *state.Private != binding) {
		return 0, ErrSlackBinding
	}
	return state.Generation, nil
}

// ImportSlack atomically creates a local task and its Slack event identity.
func (s Tasks) ImportSlack(ctx context.Context, binding SlackBinding, eventID, goal string) (bool, error) {
	return s.ImportPrivateSlack(ctx, binding, eventID, goal, 0)
}

// ImportPrivateSlack can fence a long-lived connector to its startup generation.
func (s Tasks) ImportPrivateSlack(ctx context.Context, binding SlackBinding, eventID, goal string, generation int64) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var mode string
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT mode, generation FROM slack_connection WHERE singleton FOR UPDATE`).
		Scan(&mode, &current); err != nil {
		return false, err
	}
	if mode != "private" || (generation != 0 && current != generation) {
		return false, ErrSlackBinding
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO slack_intake_binding (app_id, workspace_id, operator_id, channel_id)
		VALUES ($1, $2, $3, $4) ON CONFLICT (singleton) DO NOTHING`,
		binding.AppID, binding.WorkspaceID, binding.OperatorID, binding.ChannelID); err != nil {
		return false, err
	}
	if err := readSlackBinding(tx.QueryRowContext(ctx, slackBindingQuery+" FOR UPDATE"), binding); err != nil {
		return false, err
	}

	return importSlackGoal(ctx, tx, binding, eventID, goal)
}

// ImportOfficialSlack checks the active connection under the same lock used
// by disconnect and commits the event identity with the task.
func (s Tasks) ImportOfficialSlack(ctx context.Context, generation int64, pairingID string, binding SlackBinding, eventID, goal string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	state, err := readSlackConnection(ctx, tx, true)
	if err != nil {
		return false, err
	}
	p := state.Pairing
	if state.Mode != "official" || state.Generation != generation || p == nil ||
		p.ID != pairingID || p.Binding != binding || !p.RelayClaimed {
		return false, ErrSlackBinding
	}
	return importSlackGoal(ctx, tx, binding, eventID, goal)
}

func importSlackGoal(ctx context.Context, tx *sql.Tx, binding SlackBinding, eventID, goal string) (bool, error) {
	var savedGoal string
	err := tx.QueryRowContext(ctx, `
		SELECT t.goal FROM slack_task_sources s JOIN tasks t ON t.id = s.task_id
		WHERE s.app_id = $1 AND s.workspace_id = $2 AND s.event_id = $3`,
		binding.AppID, binding.WorkspaceID, eventID).Scan(&savedGoal)
	if err == nil {
		if savedGoal != goal {
			return false, ErrSlackReplay
		}
		return false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	task, err := createTask(ctx, tx, goal)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO slack_task_sources (app_id, workspace_id, event_id, task_id) VALUES ($1, $2, $3, $4)`,
		binding.AppID, binding.WorkspaceID, eventID, task.ID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
