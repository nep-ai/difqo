package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const (
	leaseSeconds = 30
	maxAttempts  = 3
)

type Task struct {
	ID       int64          `json:"id"`
	Goal     string         `json:"goal"`
	Status   string         `json:"status"`
	Decision *Decision      `json:"decision,omitempty"`
	Run      *SpecialistRun `json:"specialist_run,omitempty"`
	Attempt  *Attempt       `json:"attempt,omitempty"`
}

type Attempt struct {
	ID             int64      `json:"id"`
	Status         string     `json:"status,omitempty"`
	Number         int        `json:"number"`
	LeaseExpiresAt time.Time  `json:"lease_expires_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	Result         *Result    `json:"result,omitempty"`
}

type Result struct {
	ExitCode  int       `json:"exit_code"`
	Output    string    `json:"output"`
	Blocked   bool      `json:"blocked,omitempty"`
	Artifacts string    `json:"artifacts,omitempty"`
	Snapshot  *Snapshot `json:"snapshot,omitempty"`
}

type Tasks struct {
	DB *sql.DB
}

func (s Tasks) Create(ctx context.Context, goal string) (Task, error) {
	task := Task{Goal: goal, Status: "queued"}
	var decisionID *int64
	var decisionBody *string
	err := s.DB.QueryRowContext(ctx, `
		WITH saved AS (
			INSERT INTO tasks (goal, decision_id)
			VALUES ($1, (SELECT id FROM project_decisions WHERE project_id = '' ORDER BY id DESC LIMIT 1))
			RETURNING id, decision_id
		)
		SELECT saved.id, d.id, d.body FROM saved LEFT JOIN project_decisions d ON d.id = saved.decision_id`, goal).
		Scan(&task.ID, &decisionID, &decisionBody)
	if err == nil && decisionID != nil {
		task.Decision = &Decision{ID: *decisionID, Body: *decisionBody}
	}
	return task, err
}

const taskQuery = `
		SELECT t.id, t.goal,
			CASE
				WHEN a.blocked THEN 'blocked'
				WHEN a.completed_at IS NOT NULL THEN 'completed'
				WHEN a.lease_expires_at > statement_timestamp() THEN 'claimed'
				WHEN a.attempt_number = $1 THEN 'exhausted'
				ELSE 'queued'
			END,
			a.id, a.attempt_number, a.lease_expires_at, a.completed_at, a.exit_code, a.output, a.blocked, a.artifacts, a.snapshot, d.id, d.body, ` + runMetadata + `
		FROM tasks t LEFT JOIN project_decisions d ON d.id = t.decision_id LEFT JOIN LATERAL (
			SELECT id, attempt_number, lease_expires_at, completed_at, exit_code, output, blocked, artifacts, snapshot
			FROM attempts WHERE task_id = t.id ORDER BY attempt_number DESC LIMIT 1
		) a ON true` + runJoins

func (s Tasks) Get(ctx context.Context, id int64) (Task, error) {
	return scanTask(s.DB.QueryRowContext(ctx, taskQuery+" WHERE t.id = $2", maxAttempts, id))
}

func (s Tasks) Recent(ctx context.Context) ([]Task, error) {
	rows, err := s.DB.QueryContext(ctx, taskQuery+" ORDER BY t.id DESC LIMIT 100", maxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var task Task
	var attempt Attempt
	var attemptID *int64
	var number *int
	var leaseExpiresAt *time.Time
	var exitCode *int
	var output *string
	var blocked *bool
	var artifacts *string
	var decisionID *int64
	var decisionBody *string
	var snapshot, run []byte
	err := row.Scan(
		&task.ID, &task.Goal, &task.Status, &attemptID, &number, &leaseExpiresAt,
		&attempt.CompletedAt, &exitCode, &output, &blocked, &artifacts, &snapshot, &decisionID, &decisionBody, &run)
	if err == nil && decisionID != nil {
		task.Decision = &Decision{ID: *decisionID, Body: *decisionBody}
	}
	if err == nil && attemptID != nil {
		attempt.ID = *attemptID
		attempt.Number = *number
		attempt.LeaseExpiresAt = *leaseExpiresAt
		if attempt.CompletedAt != nil {
			attempt.Result = &Result{ExitCode: *exitCode, Output: *output, Blocked: *blocked, Artifacts: *artifacts}
			if snapshot != nil {
				err = json.Unmarshal(snapshot, &attempt.Result.Snapshot)
			}
		}
		task.Attempt = &attempt
	}
	if err == nil && run != nil {
		err = json.Unmarshal(run, &task.Run)
	}
	return task, err
}

func (s Tasks) Claim(ctx context.Context, project SpecialistProject) (Task, error) {
	for {
		task, retry, err := s.claim(ctx, project)
		if !retry {
			return task, err
		}
	}
}

func (s Tasks) claim(ctx context.Context, project SpecialistProject) (Task, bool, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Task{}, false, err
	}
	defer tx.Rollback()

	var taskID int64
	err = tx.QueryRowContext(ctx, `
		SELECT t.id FROM tasks t
		LEFT JOIN specialist_runs sr ON sr.task_id = t.id
		LEFT JOIN specialist_revisions pinned ON (pinned.specialist_id, pinned.revision) = (sr.specialist_id, sr.revision)
		LEFT JOIN specialist_proposals approved ON approved.id = pinned.proposal_id
		LEFT JOIN LATERAL (
			SELECT attempt_number, lease_expires_at, completed_at
			FROM attempts WHERE task_id = t.id ORDER BY attempt_number DESC LIMIT 1
		) a ON true
		WHERE ((sr.task_id IS NULL AND $2 = '') OR (
			sr.project->>'id' = $2 AND sr.project->>'binding' = $3
			AND approved.configuration->>'profile' = $4 AND approved.configuration->>'access' = $5
			AND EXISTS (
				SELECT 1 FROM specialists s
				JOIN specialist_revisions r ON (r.specialist_id, r.revision) = (s.id, s.revision)
				JOIN specialist_proposals p ON p.id = r.proposal_id
				WHERE s.id = sr.specialist_id AND s.enabled
				AND p.configuration->>'profile' = $4 AND p.configuration->>'access' = $5
				AND p.configuration->'projects' @> jsonb_build_array(jsonb_build_object('id', $2::text, 'binding', $3::text))
			)
		)) AND (a.attempt_number IS NULL OR (
			a.completed_at IS NULL AND a.lease_expires_at <= statement_timestamp()
			AND a.attempt_number < $1
		))
		ORDER BY t.id LIMIT 1 FOR UPDATE OF t SKIP LOCKED`,
		maxAttempts, project.ID, project.Binding, specialistProfile, specialistAccess).Scan(&taskID)
	if err != nil {
		return Task{}, false, err
	}

	task, err := scanTask(tx.QueryRowContext(ctx, taskQuery+` WHERE t.id = $2`, maxAttempts, taskID))
	if err != nil {
		return Task{}, false, err
	}
	if task.Run != nil {
		current, err := lockSpecialist(ctx, tx, task.Run.SpecialistID)
		if err != nil {
			return Task{}, false, err
		}
		if !authorizesProject(current, project) {
			return Task{}, true, nil
		}
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
	task.Status = "claimed"
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
	snapshot, err := json.Marshal(result.Snapshot)
	if err != nil {
		return err
	}
	var matched *bool
	err = s.DB.QueryRowContext(ctx, `
		WITH target AS MATERIALIZED (
			SELECT id, completed_at, exit_code, output, blocked, artifacts, snapshot, lease_expires_at
			FROM attempts WHERE id = $1 FOR UPDATE
		), completed AS (
			UPDATE attempts a SET completed_at = clock_timestamp(), exit_code = $2, output = $3, blocked = $4, artifacts = $5, snapshot = NULLIF($6::jsonb, 'null')
			FROM target t
			WHERE a.id = t.id AND t.completed_at IS NULL AND t.lease_expires_at > clock_timestamp()
			RETURNING a.id
		)
		SELECT CASE WHEN t.completed_at IS NOT NULL THEN t.exit_code = $2 AND t.output = $3 AND t.blocked = $4 AND t.artifacts = $5
				AND t.snapshot IS NOT DISTINCT FROM NULLIF($6::jsonb, 'null')
			WHEN c.id IS NOT NULL THEN true END
		FROM target t LEFT JOIN completed c USING (id)`, id, result.ExitCode, result.Output, result.Blocked, result.Artifacts, snapshot).Scan(&matched)
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
