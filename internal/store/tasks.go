package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Task struct {
	ID      int64    `json:"id"`
	Goal    string   `json:"goal"`
	Attempt *Attempt `json:"attempt,omitempty"`
}

type Attempt struct {
	ID          int64      `json:"id"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Result      *Result    `json:"result,omitempty"`
}

type Result struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

type Tasks struct {
	DB *sql.DB
}

func (s Tasks) Create(ctx context.Context, goal string) (Task, error) {
	task := Task{Goal: goal}
	err := s.DB.QueryRowContext(ctx,
		"INSERT INTO tasks (goal) VALUES ($1) RETURNING id", goal).Scan(&task.ID)
	return task, err
}

func (s Tasks) Get(ctx context.Context, id int64) (Task, error) {
	var task Task
	var attemptID *int64
	var completedAt *time.Time
	var exitCode *int
	var output *string
	err := s.DB.QueryRowContext(ctx, `
		SELECT t.id, t.goal, a.id, a.completed_at, a.exit_code, a.output
		FROM tasks t LEFT JOIN attempts a ON a.task_id = t.id
		WHERE t.id = $1`, id).Scan(&task.ID, &task.Goal, &attemptID, &completedAt, &exitCode, &output)
	if err == nil && attemptID != nil {
		task.Attempt = &Attempt{ID: *attemptID, CompletedAt: completedAt}
		if completedAt != nil {
			task.Attempt.Result = &Result{ExitCode: *exitCode, Output: *output}
		}
	}
	return task, err
}

func (s Tasks) Claim(ctx context.Context) (Task, error) {
	var task Task
	var attempt Attempt
	err := s.DB.QueryRowContext(ctx, `
		WITH next AS (
			SELECT id, goal FROM tasks t
			WHERE NOT EXISTS (SELECT 1 FROM attempts a WHERE a.task_id = t.id)
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
		), claimed AS (
			INSERT INTO attempts (task_id) SELECT id FROM next
			ON CONFLICT (task_id) DO NOTHING
			RETURNING id, task_id
		)
		SELECT next.id, next.goal, claimed.id FROM next JOIN claimed ON next.id = claimed.task_id`,
	).Scan(&task.ID, &task.Goal, &attempt.ID)
	if err == nil {
		task.Attempt = &attempt
	}
	return task, err
}

var ErrConflictingResult = errors.New("attempt already has a different result")

func (s Tasks) Complete(ctx context.Context, id int64, result Result) error {
	var matched bool
	err := s.DB.QueryRowContext(ctx, `
		UPDATE attempts SET
			completed_at = COALESCE(completed_at, clock_timestamp()),
			exit_code = COALESCE(exit_code, $2),
			output = COALESCE(output, $3)
		WHERE id = $1
		RETURNING exit_code = $2 AND output = $3`, id, result.ExitCode, result.Output).Scan(&matched)
	if err == nil && !matched {
		return ErrConflictingResult
	}
	return err
}
