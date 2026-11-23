package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrSlackTransition = errors.New("Slack connection changed or cannot perform that action")

type SlackPairing struct {
	ID        string    `json:"id"`
	Proof     string    `json:"proof"`
	Relay     string    `json:"relay"`
	Expires   time.Time `json:"expires"`
	State     string    `json:"state"`
	Workspace string    `json:"workspace,omitempty"`
	Installed string    `json:"installed,omitempty"`

	Operator string       `json:"operator,omitempty"`
	Binding  SlackBinding `json:"binding"`
}

type SlackConnection struct {
	Mode       string
	Generation int64
	Pairing    *SlackPairing
	Private    *SlackBinding
}

const slackConnectionQuery = `SELECT mode, generation, pairing FROM slack_connection WHERE singleton`

func (s Tasks) SlackConnection(ctx context.Context) (SlackConnection, error) {
	return readSlackConnection(ctx, s.DB, false)
}

type slackQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readSlackConnection(ctx context.Context, db slackQuerier, lock bool) (SlackConnection, error) {
	query := slackConnectionQuery
	if lock {
		query += " FOR UPDATE"
	}
	var state SlackConnection
	var pairing []byte
	if err := db.QueryRowContext(ctx, query).Scan(&state.Mode, &state.Generation, &pairing); err != nil {
		return state, err
	}
	if pairing != nil {
		if err := json.Unmarshal(pairing, &state.Pairing); err != nil {
			return state, err
		}
	}
	var private SlackBinding
	err := db.QueryRowContext(ctx, slackBindingQuery).
		Scan(&private.AppID, &private.WorkspaceID, &private.OperatorID, &private.ChannelID)
	if err == nil {
		state.Private = &private
	} else if !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	return state, nil
}

// UpdateSlackConnection serializes connection changes with private intake.
func (s Tasks) UpdateSlackConnection(ctx context.Context, change func(*SlackConnection) error) (SlackConnection, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SlackConnection{}, err
	}
	defer tx.Rollback()
	state, err := readSlackConnection(ctx, tx, true)
	if err != nil {
		return state, err
	}
	if err := change(&state); err != nil {
		return state, err
	}
	pairing, err := json.Marshal(state.Pairing)
	if err != nil {
		return state, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE slack_connection SET mode = $1, generation = $2,
		pairing = NULLIF($3::jsonb, 'null') WHERE singleton`, state.Mode, state.Generation, pairing)
	if err != nil {
		return state, err
	}
	return state, tx.Commit()
}
