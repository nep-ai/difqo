package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	leaseSeconds = 30
	maxAttempts  = 3
)

type Task struct {
	ID      int64    `json:"id"`
	Goal    string   `json:"goal"`
	Status  string   `json:"status"`
	Attempt *Attempt `json:"attempt,omitempty"`
}

type Attempt struct {
	ID             int64      `json:"id"`
	Number         int        `json:"number"`
	LeaseExpiresAt time.Time  `json:"lease_expires_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	Result         *Result    `json:"result,omitempty"`
}

type Result struct {
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
	Blocked   bool   `json:"blocked,omitempty"`
	Artifacts string `json:"artifacts,omitempty"`
}

type Tasks struct {
	DB *sql.DB
}

func (s Tasks) Create(ctx context.Context, goal string) (Task, error) {
	task := Task{Goal: goal, Status: "queued"}
	err := s.DB.QueryRowContext(ctx,
		"INSERT INTO tasks (goal) VALUES ($1) RETURNING id", goal).Scan(&task.ID)
	return task, err
}

func (s Tasks) Get(ctx context.Context, id int64) (Task, error) {
	var task Task
	var attempt Attempt
	var attemptID *int64
	var number *int
	var leaseExpiresAt *time.Time
	var exitCode *int
	var output *string
	var blocked *bool
	var artifacts *string
	err := s.DB.QueryRowContext(ctx, `
		SELECT t.id, t.goal,
			CASE
				WHEN a.blocked THEN 'blocked'
				WHEN a.completed_at IS NOT NULL THEN 'completed'
				WHEN a.lease_expires_at > statement_timestamp() THEN 'claimed'
				WHEN a.attempt_number = $2 THEN 'exhausted'
				ELSE 'queued'
			END,
			a.id, a.attempt_number, a.lease_expires_at, a.completed_at, a.exit_code, a.output, a.blocked, a.artifacts
		FROM tasks t LEFT JOIN LATERAL (
			SELECT id, attempt_number, lease_expires_at, completed_at, exit_code, output, blocked, artifacts
			FROM attempts WHERE task_id = t.id ORDER BY attempt_number DESC LIMIT 1
		) a ON true
		WHERE t.id = $1`, id, maxAttempts).Scan(
		&task.ID, &task.Goal, &task.Status, &attemptID, &number, &leaseExpiresAt,
		&attempt.CompletedAt, &exitCode, &output, &blocked, &artifacts)
	if err == nil && attemptID != nil {
		attempt.ID = *attemptID
		attempt.Number = *number
		attempt.LeaseExpiresAt = *leaseExpiresAt
		if attempt.CompletedAt != nil {
			attempt.Result = &Result{ExitCode: *exitCode, Output: *output, Blocked: *blocked, Artifacts: *artifacts}
		}
		task.Attempt = &attempt
	}
	return task, err
}

func (s Tasks) Claim(ctx context.Context) (Task, error) {
	for {
		task, retry, err := s.claim(ctx)
		if !retry {
			return task, err
		}
	}
}

func (s Tasks) claim(ctx context.Context) (Task, bool, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Task{}, false, err
	}
	defer tx.Rollback()

	task := Task{Status: "claimed"}
	err = tx.QueryRowContext(ctx, `
		SELECT t.id, t.goal FROM tasks t
		LEFT JOIN LATERAL (
			SELECT attempt_number, lease_expires_at, completed_at
			FROM attempts WHERE task_id = t.id ORDER BY attempt_number DESC LIMIT 1
		) a ON true
		WHERE a.attempt_number IS NULL OR (
			a.completed_at IS NULL AND a.lease_expires_at <= statement_timestamp()
			AND a.attempt_number < $1
		)
		ORDER BY t.id LIMIT 1 FOR UPDATE OF t SKIP LOCKED`, maxAttempts).Scan(&task.ID, &task.Goal)
	if err != nil {
		return Task{}, false, err
	}

	// Recheck with a fresh snapshot after taking the task lock. Lock the
	// latest attempt as well to serialize with any in-flight renewal or result.
	var attempt Attempt
	err = tx.QueryRowContext(ctx, `
		WITH latest AS MATERIALIZED (
			SELECT attempt_number, lease_expires_at, completed_at
			FROM attempts WHERE task_id = $1
			ORDER BY attempt_number DESC LIMIT 1 FOR UPDATE
		)
		INSERT INTO attempts (task_id, attempt_number, lease_expires_at)
		SELECT $1, COALESCE((SELECT attempt_number FROM latest), 0) + 1,
			clock_timestamp() + make_interval(secs => $2)
		WHERE NOT EXISTS (
			SELECT 1 FROM latest
			WHERE completed_at IS NOT NULL OR lease_expires_at > clock_timestamp()
				OR attempt_number >= $3
		)
		RETURNING id, attempt_number, lease_expires_at`, task.ID, leaseSeconds, maxAttempts).
		Scan(&attempt.ID, &attempt.Number, &attempt.LeaseExpiresAt)
	if err != nil {
		return Task{}, errors.Is(err, sql.ErrNoRows), err
	}
	task.Attempt = &attempt
	return task, false, tx.Commit()
}

var (
	ErrConflictingResult = errors.New("attempt already has a different result")
	ErrInactiveLease     = errors.New("attempt lease is no longer active")
)

func (s Tasks) Renew(ctx context.Context, id int64) error {
	var renewed bool
	err := s.DB.QueryRowContext(ctx, `
		WITH target AS MATERIALIZED (
			SELECT id, completed_at, lease_expires_at FROM attempts WHERE id = $1 FOR UPDATE
		), renewed AS (
			UPDATE attempts a SET lease_expires_at = clock_timestamp() + make_interval(secs => $2)
			FROM target t
			WHERE a.id = t.id AND t.completed_at IS NULL AND t.lease_expires_at > clock_timestamp()
			RETURNING a.id
		)
		SELECT renewed.id IS NOT NULL FROM target LEFT JOIN renewed USING (id)`, id, leaseSeconds).Scan(&renewed)
	if err == nil && !renewed {
		return ErrInactiveLease
	}
	return err
}

func (s Tasks) Complete(ctx context.Context, id int64, result Result) error {
	var matched *bool
	err := s.DB.QueryRowContext(ctx, `
		WITH target AS MATERIALIZED (
			SELECT id, completed_at, exit_code, output, blocked, artifacts, lease_expires_at
			FROM attempts WHERE id = $1 FOR UPDATE
		), completed AS (
			UPDATE attempts a SET completed_at = clock_timestamp(), exit_code = $2, output = $3, blocked = $4, artifacts = $5
			FROM target t
			WHERE a.id = t.id AND t.completed_at IS NULL AND t.lease_expires_at > clock_timestamp()
			RETURNING a.id
		)
		SELECT CASE WHEN t.completed_at IS NOT NULL THEN t.exit_code = $2 AND t.output = $3 AND t.blocked = $4 AND t.artifacts = $5
			WHEN c.id IS NOT NULL THEN true END
		FROM target t LEFT JOIN completed c USING (id)`, id, result.ExitCode, result.Output, result.Blocked, result.Artifacts).Scan(&matched)
	if err != nil {
		return err
	}
	if matched == nil {
		return ErrInactiveLease
	}
	if !*matched {
		return ErrConflictingResult
	}
	return nil
}
