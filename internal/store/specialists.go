package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrSpecialistStale = errors.New("specialist proposal is stale")
var ErrSpecialistProject = errors.New("select at least one registered project")

type SpecialistProject struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Label            string `json:"label"`
	Binding          string `json:"binding"`
	ExecutionBinding string `json:"execution_binding,omitempty"`
	Checkout         string `json:"-"`
	Check            string `json:"-"`
	Version          string `json:"-"`
}

type SpecialistConfig struct {
	Name     string              `json:"name"`
	Report   SnapshotReport      `json:"report,omitempty"`
	Purpose  string              `json:"purpose"`
	Output   string              `json:"output"`
	Profile  string              `json:"profile"`
	Access   string              `json:"access"`
	Projects []SpecialistProject `json:"projects"`
}

type SnapshotReport string

const (
	ReportBoth      SnapshotReport = "both"
	ReportChanges   SnapshotReport = "changes"
	ReportInventory SnapshotReport = "inventory"
)

func (r SnapshotReport) Valid() bool {
	return r == "" || r == ReportBoth || r == ReportChanges || r == ReportInventory
}

const specialistProfile = "snapshot-review-v1"
const specialistAccess = "Inspect the selected project snapshot and report findings. Repository writes and external integrations are outside this scope."

type SpecialistProposal struct {
	ID           string           `json:"id"`
	SpecialistID int64            `json:"specialist_id"`
	BaseRevision int              `json:"base_revision"`
	Action       string           `json:"action"`
	Config       SpecialistConfig `json:"configuration"`
	Outcome      string           `json:"outcome"`
	CreatedAt    time.Time        `json:"created_at"`
	DecidedAt    *time.Time       `json:"decided_at"`
}

type Specialist struct {
	ID       int64            `json:"id"`
	Revision int              `json:"revision"`
	Enabled  bool             `json:"enabled"`
	Config   SpecialistConfig `json:"configuration"`
}

type Specialists struct {
	DB       *sql.DB
	Projects []SpecialistProject
}

type SpecialistState struct {
	Projects    []SpecialistProject  `json:"projects"`
	Specialists []Specialist         `json:"specialists"`
	Proposals   []SpecialistProposal `json:"proposals"`
	Access      string               `json:"access"`
}

const specialistQuery = `
	SELECT s.id, s.revision, s.enabled, p.configuration
	FROM specialists s
	JOIN specialist_revisions r ON r.specialist_id = s.id AND r.revision = s.revision
	JOIN specialist_proposals p ON p.id = r.proposal_id`

const proposalFields = `id, coalesce(specialist_id, 0), base_revision, action, configuration, coalesce(outcome, ''), created_at, decided_at`

func scanSpecialist(row interface{ Scan(...any) error }) (Specialist, error) {
	var s Specialist
	var config []byte
	if err := row.Scan(&s.ID, &s.Revision, &s.Enabled, &config); err != nil {
		return s, err
	}
	err := json.Unmarshal(config, &s.Config)
	return s, err
}

func scanProposal(row interface{ Scan(...any) error }) (SpecialistProposal, error) {
	var p SpecialistProposal
	var config []byte
	if err := row.Scan(&p.ID, &p.SpecialistID, &p.BaseRevision, &p.Action, &config, &p.Outcome, &p.CreatedAt, &p.DecidedAt); err != nil {
		return p, err
	}
	err := json.Unmarshal(config, &p.Config)
	return p, err
}

func (s Specialists) State(ctx context.Context) (SpecialistState, error) {
	state := SpecialistState{
		Projects: append([]SpecialistProject{}, s.Projects...), Access: specialistAccess,
		Specialists: []Specialist{}, Proposals: []SpecialistProposal{},
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return state, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, specialistQuery+` ORDER BY s.id`)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		current, err := scanSpecialist(rows)
		if err != nil {
			rows.Close()
			return state, err
		}
		state.Specialists = append(state.Specialists, current)
	}
	if err := rows.Err(); err != nil {
		return state, err
	}

	rows, err = tx.QueryContext(ctx, `SELECT `+proposalFields+` FROM specialist_proposals ORDER BY created_at, id`)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	currentByID := make(map[int64]Specialist, len(state.Specialists))
	for _, current := range state.Specialists {
		currentByID[current.ID] = current
	}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return state, err
		}
		if p.Outcome == "" && s.stale(p, currentByID[p.SpecialistID]) {
			p.Outcome = "stale"
		}
		state.Proposals = append(state.Proposals, p)
	}
	if err := rows.Err(); err != nil {
		return state, err
	}
	return state, tx.Commit()
}

func (s Specialists) Eligible(ctx context.Context, specialistID int64, projectID string) (bool, error) {
	current, err := scanSpecialist(s.DB.QueryRowContext(ctx, specialistQuery+` WHERE s.id = $1`, specialistID))
	if err != nil {
		return false, err
	}
	project, found := s.Project(projectID)
	return found && authorizesProject(current, project), nil
}

func (s Specialists) hasBinding(project SpecialistProject) bool {
	for _, registered := range s.Projects {
		if registered.ID == project.ID && registered.Binding == project.Binding {
			return true
		}
	}
	return false
}

func (s Specialists) stale(p SpecialistProposal, current Specialist) bool {
	if p.Action != "create" && (!current.Enabled || current.Revision != p.BaseRevision) {
		return true
	}
	if p.Action != "disable" {
		if p.Config.Profile != specialistProfile || p.Config.Access != specialistAccess {
			return true
		}
		for _, project := range p.Config.Projects {
			if !s.hasBinding(project) {
				return true
			}
		}
	}
	return false
}

func (s Specialists) Propose(ctx context.Context, p SpecialistProposal, projectIDs []string) (SpecialistProposal, error) {
	if p.Action != "create" {
		current, err := scanSpecialist(s.DB.QueryRowContext(ctx, specialistQuery+` WHERE s.id = $1`, p.SpecialistID))
		if err != nil {
			return p, err
		}
		if p.Action == "disable" {
			p.Config = current.Config
		}
	}
	if p.Action != "disable" {
		p.Config.Profile = specialistProfile
		p.Config.Access = specialistAccess
		for _, registered := range s.Projects {
			for _, id := range projectIDs {
				if id == registered.ID {
					p.Config.Projects = append(p.Config.Projects, registered)
				}
			}
		}
		if len(p.Config.Projects) == 0 || len(p.Config.Projects) != len(projectIDs) {
			return p, ErrSpecialistProject
		}
	}
	config, err := json.Marshal(p.Config)
	if err != nil {
		return p, err
	}
	// The client retains the UUID after an uncertain response. Identical retries
	// return the original proposal. A changed draft needs a different UUID.
	return scanProposal(s.DB.QueryRowContext(ctx, `
		INSERT INTO specialist_proposals AS p (id, specialist_id, base_revision, action, configuration)
		VALUES ($1, nullif($2, 0), $3, $4, $5::jsonb)
		ON CONFLICT (id) DO UPDATE SET id = p.id
		WHERE p.action = excluded.action AND (p.specialist_id IS NOT DISTINCT FROM excluded.specialist_id OR p.action = 'create')
			AND p.base_revision = excluded.base_revision AND p.configuration = excluded.configuration
		RETURNING `+proposalFields, p.ID, p.SpecialistID, p.BaseRevision, p.Action, string(config)))
}

func (s Specialists) Decide(ctx context.Context, id, outcome string) (SpecialistProposal, error) {
	var p SpecialistProposal
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	p, err = scanProposal(tx.QueryRowContext(ctx, `SELECT `+proposalFields+` FROM specialist_proposals WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return p, err
	}
	if p.Outcome == outcome {
		return p, nil
	}
	if p.Outcome != "" {
		return p, ErrSpecialistStale
	}
	if outcome == "approve" {
		var current Specialist
		if p.Action != "create" {
			err = tx.QueryRowContext(ctx, `SELECT revision, enabled FROM specialists WHERE id = $1 FOR UPDATE`, p.SpecialistID).
				Scan(&current.Revision, &current.Enabled)
			if err != nil {
				return p, err
			}
		}
		if s.stale(p, current) {
			return p, ErrSpecialistStale
		}
		if err := approveSpecialist(ctx, tx, &p); err != nil {
			return p, err
		}
	}
	err = tx.QueryRowContext(ctx, `
		UPDATE specialist_proposals SET specialist_id = nullif($2, 0), outcome = $3, decided_at = clock_timestamp()
		WHERE id = $1 RETURNING decided_at`, p.ID, p.SpecialistID, outcome).Scan(&p.DecidedAt)
	if err != nil {
		return p, err
	}
	p.Outcome = outcome
	return p, tx.Commit()
}

func approveSpecialist(ctx context.Context, tx *sql.Tx, p *SpecialistProposal) error {
	revision := p.BaseRevision + 1
	switch p.Action {
	case "create":
		if err := tx.QueryRowContext(ctx, `INSERT INTO specialists (revision) VALUES (1) RETURNING id`).Scan(&p.SpecialistID); err != nil {
			return err
		}
	case "update":
		if _, err := tx.ExecContext(ctx, `UPDATE specialists SET revision = $2 WHERE id = $1`, p.SpecialistID, revision); err != nil {
			return err
		}
	case "disable":
		_, err := tx.ExecContext(ctx, `UPDATE specialists SET enabled = false WHERE id = $1`, p.SpecialistID)
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO specialist_revisions (specialist_id, revision, proposal_id) VALUES ($1, $2, $3)`,
		p.SpecialistID, revision, p.ID)
	return err
}
