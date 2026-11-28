package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrRunUnauthorized = errors.New("the selected specialist revision is unavailable for this project")
var ErrRunConflict = errors.New("this run request already belongs to a different goal or selection")

type SpecialistRun struct {
	Report       SnapshotReport    `json:"report,omitempty"`
	SpecialistID int64             `json:"specialist_id"`
	Revision     int               `json:"revision"`
	Project      SpecialistProject `json:"project"`
	Name         string            `json:"name"`
	Purpose      string            `json:"purpose"`
	Output       string            `json:"output"`
	Profile      string            `json:"profile"`
	Access       string            `json:"access"`
}

const runMetadata = `CASE WHEN sr.task_id IS NOT NULL THEN jsonb_build_object(
	'specialist_id', sr.specialist_id, 'revision', sr.revision, 'project', sr.project,
	'name', approved.configuration->>'name', 'purpose', approved.configuration->>'purpose',
	'output', approved.configuration->>'output', 'access', approved.configuration->>'access',
	'profile', approved.configuration->>'profile', 'report', approved.configuration->>'report'
) END`

const runJoins = `
	LEFT JOIN specialist_runs sr ON sr.task_id = t.id
	LEFT JOIN specialist_revisions pinned ON (pinned.specialist_id, pinned.revision) = (sr.specialist_id, sr.revision)
	LEFT JOIN specialist_proposals approved ON approved.id = pinned.proposal_id`

func (s Specialists) Project(id string) (SpecialistProject, bool) {
	for _, project := range s.ProjectCatalog() {
		if project.ID == id {
			return project, true
		}
	}
	return SpecialistProject{}, false
}

func authorizesProject(current Specialist, project SpecialistProject) bool {
	if !current.Enabled || current.Config.Profile != specialistProfile || current.Config.Access != specialistAccess {
		return false
	}
	for _, approved := range current.Config.Projects {
		if approved.ID == project.ID && approved.Binding == project.Binding {
			return true
		}
	}
	return false
}

// Lock the specialist row before resolving its current revision. Approval
// and disable take an exclusive lock on the same row in their transaction.
func lockSpecialist(ctx context.Context, tx *sql.Tx, id int64) (Specialist, error) {
	var locked int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM specialists WHERE id = $1 FOR SHARE`, id).Scan(&locked); err != nil {
		return Specialist{}, err
	}
	return scanSpecialist(tx.QueryRowContext(ctx, specialistQuery+` WHERE s.id = $1`, id))
}

func (s Specialists) Queue(ctx context.Context, requestID, goal string, id int64, revision int, project SpecialistProject) (Task, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	// Serialize retries of the same client request even if they carry
	// conflicting payloads. A collision only adds lock contention.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, requestID); err != nil {
		return Task{}, err
	}
	existing, err := scanTask(tx.QueryRowContext(ctx, taskQuery+` WHERE sr.request_id = $2`, maxAttempts, requestID))
	if err == nil {
		if existing.Goal != goal || existing.Run.SpecialistID != id || existing.Run.Revision != revision ||
			existing.Run.Project.ID != project.ID || existing.Run.Project.Binding != project.Binding {
			return Task{}, ErrRunConflict
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Task{}, err
	}
	current, err := lockSpecialist(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrRunUnauthorized
	}
	if err != nil {
		return Task{}, err
	}
	if !s.hasBinding(project) || current.Revision != revision || !authorizesProject(current, project) {
		return Task{}, ErrRunUnauthorized
	}
	var taskID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO tasks (goal, decision_id) VALUES ($1,
		(SELECT id FROM project_decisions WHERE project_id = $2 AND project_binding = $3 ORDER BY id DESC LIMIT 1))
		RETURNING id`, goal, project.ID, project.Binding).Scan(&taskID)
	if err != nil {
		return Task{}, err
	}
	data, err := json.Marshal(project)
	if err != nil {
		return Task{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO specialist_runs (task_id, request_id, specialist_id, revision, project)
		VALUES ($1, $2, $3, $4, $5::jsonb)`, taskID, requestID, id, revision, string(data))
	if err != nil {
		return Task{}, err
	}
	task, err := scanTask(tx.QueryRowContext(ctx, taskQuery+` WHERE t.id = $2`, maxAttempts, taskID))
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

type RunHistory struct {
	Attempts  []Attempt `json:"attempts"`
	Claimable bool      `json:"claimable"`
}

func (s Specialists) History(ctx context.Context, taskID int64) (RunHistory, error) {
	history := RunHistory{Attempts: []Attempt{}}
	var data []byte
	err := s.DB.QueryRowContext(ctx, `
		SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id', a.id, 'number', a.attempt_number, 'lease_expires_at', a.lease_expires_at,
			'completed_at', a.completed_at, 'status', CASE
				WHEN a.blocked THEN 'blocked'
				WHEN a.completed_at IS NOT NULL AND a.exit_code = 0 THEN 'completed'
				WHEN a.completed_at IS NOT NULL THEN 'failed'
				WHEN a.lease_expires_at <= statement_timestamp() THEN 'interrupted'
				ELSE 'claimed' END,
			'result', CASE WHEN a.completed_at IS NOT NULL THEN jsonb_build_object(
				'exit_code', a.exit_code, 'output', a.output, 'blocked', a.blocked) END
		) ORDER BY a.attempt_number) FILTER (WHERE a.id IS NOT NULL), '[]'::jsonb)
		FROM specialist_runs sr LEFT JOIN attempts a ON a.task_id = sr.task_id
		WHERE sr.task_id = $1 GROUP BY sr.task_id`, taskID).Scan(&data)
	if err != nil {
		return history, err
	}
	if err := json.Unmarshal(data, &history.Attempts); err != nil {
		return history, fmt.Errorf("load attempt history: %w", err)
	}
	task, err := (Tasks{DB: s.DB}).Get(ctx, taskID)
	if err != nil {
		return history, err
	}
	current, err := scanSpecialist(s.DB.QueryRowContext(ctx, specialistQuery+` WHERE s.id = $1`, task.Run.SpecialistID))
	if err != nil {
		return history, err
	}
	history.Claimable = task.Run.Profile == specialistProfile && task.Run.Access == specialistAccess &&
		s.hasBinding(task.Run.Project) && authorizesProject(current, task.Run.Project)
	return history, nil
}
