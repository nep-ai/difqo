package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Decision struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

func (s Tasks) LatestDecision(ctx context.Context, project SpecialistProject) (Decision, error) {
	var d Decision
	err := s.DB.QueryRowContext(ctx, "SELECT id, body FROM project_decisions WHERE project_id = $1 AND project_binding = $2 ORDER BY id DESC LIMIT 1", project.ID, project.Binding).Scan(&d.ID, &d.Body)
	return d, err
}

func (s Tasks) SaveDecision(ctx context.Context, body string) (Decision, error) {
	return s.SaveProjectDecision(ctx, body, SpecialistProject{})
}

func (s Tasks) SaveProjectDecision(ctx context.Context, body string, project SpecialistProject) (Decision, error) {
	d := Decision{Body: body}
	err := s.DB.QueryRowContext(ctx, "INSERT INTO project_decisions (body, project_id, project_binding) VALUES ($1, $2, $3) RETURNING id", body, project.ID, project.Binding).Scan(&d.ID)
	return d, err
}

type Snapshot struct {
	Base        string `json:"base"`
	Candidate   string `json:"candidate,omitempty"`
	PatchSHA256 string `json:"patch_sha256"`
	Image       string `json:"image"`
	Command     string `json:"command"`
}

type ReviewCheck struct {
	ID       int64  `json:"id"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Output   string `json:"output"`
}

type Approval struct {
	CheckID    int64     `json:"check_id"`
	ApprovedAt time.Time `json:"approved_at"`
}

type Review struct {
	Check    *ReviewCheck `json:"check,omitempty"`
	Approval *Approval    `json:"approval,omitempty"`
}

var (
	ErrReviewBusy     = errors.New("a check is already running")
	ErrReviewApproved = errors.New("this patch is already approved")
	ErrStaleCheck     = errors.New("approval requires the latest passing check for this patch")
)

func (s Tasks) Review(ctx context.Context, attemptID int64) (Review, error) {
	return readReview(ctx, s.DB, attemptID)
}

func readReview(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, attemptID int64) (Review, error) {
	var review Review
	var check ReviewCheck
	var checkID *int64
	var approval Approval
	var approvedAt *time.Time
	err := db.QueryRowContext(ctx, `
		SELECT c.id, CASE
			WHEN c.exit_code = 0 THEN 'passed'
			WHEN c.exit_code IS NOT NULL THEN 'failed'
			WHEN c.started_at + interval '60 seconds' > statement_timestamp() THEN 'running'
			ELSE 'interrupted'
		END, c.exit_code, COALESCE(c.output, ''), COALESCE(p.check_id, 0), p.approved_at
		FROM attempts a
		LEFT JOIN LATERAL (
			SELECT id, started_at, exit_code, output FROM review_checks
			WHERE attempt_id = a.id ORDER BY id DESC LIMIT 1
		) c ON true
		LEFT JOIN patch_approvals p ON p.attempt_id = a.id
		WHERE a.id = $1`, attemptID).
		Scan(&checkID, &check.Status, &check.ExitCode, &check.Output, &approval.CheckID, &approvedAt)
	if err != nil {
		return review, err
	}
	if checkID != nil {
		check.ID = *checkID
		review.Check = &check
	}
	if approvedAt != nil {
		approval.ApprovedAt = *approvedAt
		review.Approval = &approval
	}
	return review, nil
}

func (s Tasks) StartCheck(ctx context.Context, attemptID int64) (int64, error) {
	var checkID int64
	err := s.updateReview(ctx, attemptID, func(tx *sql.Tx, review Review) error {
		if review.Approval != nil {
			return ErrReviewApproved
		}
		if review.Check != nil && review.Check.Status == "running" {
			return ErrReviewBusy
		}
		return tx.QueryRowContext(ctx, "INSERT INTO review_checks (attempt_id) VALUES ($1) RETURNING id", attemptID).Scan(&checkID)
	})
	return checkID, err
}

func (s Tasks) FinishCheck(ctx context.Context, checkID int64, exitCode int, output string) error {
	var id int64
	err := s.DB.QueryRowContext(ctx, `
		UPDATE review_checks SET completed_at = clock_timestamp(), exit_code = $2, output = $3
		WHERE id = $1 AND completed_at IS NULL AND started_at + interval '60 seconds' > clock_timestamp()
		RETURNING id`, checkID, exitCode, output).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStaleCheck
	}
	return err
}

func (s Tasks) Approve(ctx context.Context, attemptID, checkID int64) error {
	return s.updateReview(ctx, attemptID, func(tx *sql.Tx, review Review) error {
		if review.Check == nil || review.Check.ID != checkID || review.Check.Status != "passed" {
			return ErrStaleCheck
		}
		if review.Approval != nil {
			return nil
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO patch_approvals (attempt_id, check_id) VALUES ($1, $2)", attemptID, checkID)
		return err
	})
}

func (s Tasks) updateReview(ctx context.Context, attemptID int64, update func(*sql.Tx, Review) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM attempts WHERE id = $1 AND snapshot IS NOT NULL FOR UPDATE", attemptID).Scan(&id); err != nil {
		return err
	}
	review, err := readReview(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := update(tx, review); err != nil {
		return err
	}
	return tx.Commit()
}
