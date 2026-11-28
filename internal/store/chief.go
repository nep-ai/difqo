package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const ChatLease = 3 * time.Minute

var ErrChatBusy = errors.New("Chief of Staff is still responding")

type ChiefAnswer struct {
	Reply string `json:"reply"`
	Goal  string `json:"proposed_goal"`
}

type ChatTurn struct {
	ID          int64        `json:"id,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
	State       string       `json:"state,omitempty"`
	Reply       string       `json:"reply,omitempty"`
	Proposal    string       `json:"proposed_goal,omitempty"`
	JobID       int64        `json:"job_id,omitempty"`
	Problem     string       `json:"problem,omitempty"`
}

const chatView = `id, message, created_at, attachment_metadata,
	CASE WHEN state = 'running' AND expires_at <= clock_timestamp() THEN 'attention' ELSE state END,
	reply, CASE WHEN state = 'running' AND expires_at <= clock_timestamp()
	THEN 'The local conversation was interrupted. You can send the message again.' ELSE problem END, COALESCE(proposed_goal, ''),
	COALESCE((SELECT task_id FROM chief_project_jobs WHERE turn_id = chief_conversation_turns.id), 0)`

func scanChat(row interface{ Scan(...any) error }) (ChatTurn, error) {
	var turn ChatTurn
	var raw []byte
	err := row.Scan(&turn.ID, &turn.Text, &turn.CreatedAt, &raw, &turn.State, &turn.Reply, &turn.Problem, &turn.Proposal, &turn.JobID)
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &turn.Attachments)
	}
	return turn, err
}

func (s Tasks) ChiefTurns(ctx context.Context) ([]ChatTurn, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+chatView+` FROM chief_conversation_turns ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	turns := []ChatTurn{}
	for rows.Next() {
		turn, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	return turns, rows.Err()
}

func (s Tasks) StartChiefTurn(ctx context.Context, requestID string, message ChatTurn) (ChatTurn, bool, error) {
	payload, err := json.Marshal(message.Attachments)
	if err != nil {
		return ChatTurn{}, false, err
	}
	var details []Attachment
	for _, file := range message.Attachments {
		details = append(details, Attachment{Name: file.Name, MIME: file.MIME})
	}
	metadata, err := json.Marshal(details)
	if err != nil {
		return ChatTurn{}, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ChatTurn{}, false, err
	}
	defer tx.Rollback()
	// Serializes submissions across local servers sharing this database.
	if _, err := tx.ExecContext(ctx, `LOCK TABLE chief_conversation_turns IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return ChatTurn{}, false, err
	}
	saved, err := scanChat(tx.QueryRowContext(ctx, `SELECT `+chatView+` FROM chief_conversation_turns WHERE request_id = $1`, requestID))
	if err == nil {
		return saved, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ChatTurn{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chief_conversation_turns SET state = 'attention',
		problem = 'The local conversation was interrupted. You can send the message again.'
		WHERE state = 'running' AND expires_at <= clock_timestamp()`); err != nil {
		return ChatTurn{}, false, err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM chief_conversation_turns WHERE state = 'running')`).Scan(&active); err != nil {
		return ChatTurn{}, false, err
	}
	if active {
		return ChatTurn{}, false, ErrChatBusy
	}
	saved, err = scanChat(tx.QueryRowContext(ctx, `INSERT INTO chief_conversation_turns
		(request_id, message, attachment_metadata, attachments, state, expires_at)
		VALUES ($1, $2, $3, $4, 'running', clock_timestamp() + make_interval(secs => $5)) RETURNING `+chatView,
		requestID, message.Text, metadata, payload, int(ChatLease.Seconds())))
	if err != nil {
		return ChatTurn{}, false, err
	}
	return saved, true, tx.Commit()
}

func (s Tasks) FinishChiefTurn(ctx context.Context, id int64, reply, problem, goal string) error {
	state := "completed"
	if problem != "" {
		state = "attention"
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE chief_conversation_turns SET state = $2, reply = $3, problem = $4, proposed_goal = NULLIF($5, '')
		WHERE id = $1 AND state = 'running' AND expires_at > clock_timestamp()`, id, state, reply, problem, goal)
	return err
}

func (s Tasks) ChiefAttachments(ctx context.Context, id int64) ([]Attachment, error) {
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT attachments FROM chief_conversation_turns WHERE id = $1 AND state = 'completed'`, id).Scan(&raw); err != nil {
		return nil, err
	}
	var files []Attachment
	err := json.Unmarshal(raw, &files)
	return files, err
}
