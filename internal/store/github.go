package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrGitHubEnrollment = errors.New("GitHub repository is already enrolled with a different owner or start time")

type GitHubEnrollment struct {
	RepositoryID int64
	OwnerID      int64
	Start        time.Time
}

type GitHubTask struct {
	CommentID    int64
	ThreadNumber int64
	Goal         string
}

func (s Tasks) GitHubCursor(ctx context.Context, enrollment GitHubEnrollment) (time.Time, error) {
	var owner int64
	var start, through time.Time
	err := s.DB.QueryRowContext(ctx, `
		SELECT owner_id, enrolled_at, updated_through
		FROM github_intake_cursors WHERE repository_id = $1`, enrollment.RepositoryID).Scan(&owner, &start, &through)
	if errors.Is(err, sql.ErrNoRows) {
		return enrollment.Start, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if owner != enrollment.OwnerID || !start.Equal(enrollment.Start) {
		return time.Time{}, ErrGitHubEnrollment
	}
	return through, nil
}

func (s Tasks) ImportGitHub(ctx context.Context, enrollment GitHubEnrollment, through time.Time, comments []GitHubTask) (int, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Serialize imports for this repository, including concurrent first polls.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO github_intake_cursors (repository_id, owner_id, enrolled_at, updated_through)
		VALUES ($1, $2, $3, $3) ON CONFLICT (repository_id) DO NOTHING`,
		enrollment.RepositoryID, enrollment.OwnerID, enrollment.Start); err != nil {
		return 0, err
	}
	var owner int64
	var start time.Time
	if err := tx.QueryRowContext(ctx, `
		SELECT owner_id, enrolled_at FROM github_intake_cursors
		WHERE repository_id = $1 FOR UPDATE`, enrollment.RepositoryID).Scan(&owner, &start); err != nil {
		return 0, err
	}
	if owner != enrollment.OwnerID || !start.Equal(enrollment.Start) {
		return 0, ErrGitHubEnrollment
	}

	var imported int
	for _, comment := range comments {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM github_task_sources WHERE repository_id = $1 AND comment_id = $2)`,
			enrollment.RepositoryID, comment.CommentID).Scan(&exists); err != nil {
			return 0, err
		}
		if exists {
			continue
		}
		task, err := createTask(ctx, tx, comment.Goal)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO github_task_sources (repository_id, comment_id, thread_number, author_id, task_id)
			VALUES ($1, $2, $3, $4, $5)`, enrollment.RepositoryID, comment.CommentID,
			comment.ThreadNumber, owner, task.ID); err != nil {
			return 0, err
		}
		imported++
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE github_intake_cursors SET updated_through = GREATEST(updated_through, $2)
		WHERE repository_id = $1`, enrollment.RepositoryID, through); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return imported, nil
}
