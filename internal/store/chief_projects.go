package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

var ErrChiefProjectConflict = errors.New("this request is already confirmed for another project or no longer has a coding proposal")

func (s Tasks) ConfirmChiefProject(ctx context.Context, turnID int64, project SpecialistProject) (Task, error) {
	if project.ID == "" || project.ExecutionBinding == "" {
		return Task{}, ErrChiefProjectConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	var goal string
	var files, metadata []byte
	var hasFiles bool
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(proposed_goal, ''), attachments, attachment_metadata,
			CASE WHEN jsonb_typeof(attachments) = 'array' THEN jsonb_array_length(attachments) > 0 ELSE false END
		FROM chief_conversation_turns WHERE id = $1 AND state = 'completed'
		FOR UPDATE`, turnID).Scan(&goal, &files, &metadata, &hasFiles)
	if err != nil {
		return Task{}, err
	}
	if goal == "" {
		return Task{}, ErrChiefProjectConflict
	}
	task, err := scanTask(tx.QueryRowContext(ctx, taskQuery+
		` WHERE t.id = (SELECT task_id FROM chief_project_jobs WHERE turn_id = $2)`, maxAttempts, turnID))
	if err == nil {
		if task.ChiefProject == nil || task.ChiefProject.ID != project.ID || task.ChiefProject.ExecutionBinding != project.ExecutionBinding {
			return Task{}, ErrChiefProjectConflict
		}
		return task, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Task{}, err
	}

	if !hasFiles {
		// The conversation passes a file-only turn into the next follow-up
		// when that follow-up has no attachments of its own.
		err := tx.QueryRowContext(ctx, `
			SELECT attachments, attachment_metadata FROM (
				SELECT message, attachments, attachment_metadata
				FROM chief_conversation_turns WHERE id < $1 AND state = 'completed' ORDER BY id DESC LIMIT 1
			) previous WHERE message = ''`, turnID).Scan(&files, &metadata)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Task{}, err
		}
	}

	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO tasks (goal, decision_id, attachment_metadata, attachments)
		VALUES ($1, (SELECT id FROM project_decisions WHERE project_id = $2 AND project_binding = $3 ORDER BY id DESC LIMIT 1),
			NULLIF($4::jsonb, 'null'), NULLIF($5::jsonb, 'null')) RETURNING id`,
		goal, project.ID, project.Binding, metadata, files).Scan(&id)
	if err != nil {
		return Task{}, err
	}
	data, err := json.Marshal(project)
	if err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chief_project_jobs (task_id, turn_id, project) VALUES ($1, $2, $3)`, id, turnID, data); err != nil {
		return Task{}, err
	}
	task, err = scanTask(tx.QueryRowContext(ctx, taskQuery+` WHERE t.id = $2`, maxAttempts, id))
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}
