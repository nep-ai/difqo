package slackrelay

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nep-ai/difqo/internal/slackintake"
)

type PilotConfig struct {
	Origin, AppID, ClientID, ClientSecret, SigningSecret string
	WorkspaceID, OperatorID, EnrollmentToken             string
	TokenKey                                             [32]byte
}

type pilotConfig struct {
	workspace, operator string
	enrollmentHash      [sha256.Size]byte
}

// NewPilot pins all provider calls to Slack's public origin.
func NewPilot(db *pgxpool.Pool, config PilotConfig) (http.Handler, error) {
	return newPilot(db, config, "https://slack.com")
}

func newPilot(db *pgxpool.Pool, config PilotConfig, slackOrigin string) (http.Handler, error) {
	origin, err := slackintake.HTTPSRelayOrigin(config.Origin)
	if err != nil {
		return nil, err
	}
	if !workspaceID.MatchString(config.WorkspaceID) || !operatorID.MatchString(config.OperatorID) ||
		!proofID.MatchString(config.EnrollmentToken) {
		return nil, errors.New("Slack pilot requires a workspace ID, operator ID, and 32-byte hex enrollment token")
	}
	pilot := &pilotConfig{workspace: config.WorkspaceID, operator: config.OperatorID,
		enrollmentHash: sha256.Sum256([]byte(config.EnrollmentToken))}
	return newRelay(db, config.TokenKey, origin, slackOrigin, config.ClientID, config.ClientSecret, config.AppID, config.SigningSecret, time.Now, pilot)
}

func (p *pilotConfig) allows(kind flowKind, who identity, installed installation) bool {
	if kind == signIn {
		return who.Workspace == p.workspace && who.Operator == p.operator
	}
	return installed.workspace == p.workspace
}

func (r *relay) enroll(w http.ResponseWriter, req *http.Request) {
	token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	hash := sha256.Sum256([]byte(token))
	if !ok || !proofID.MatchString(token) || subtle.ConstantTimeCompare(hash[:], r.pilot.enrollmentHash[:]) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var input struct {
		ID    string `json:"pairing_id"`
		Proof string `json:"proof"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF ||
		!pairingID.MatchString(input.ID) || !proofID.MatchString(input.Proof) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx := req.Context()
	s := r.pairings
	proof := sha256.Sum256([]byte(input.Proof))
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := s.register(ctx, tx, input.ID, proof[:]); err != nil {
			return err
		}
		p, err := s.lock(ctx, tx, input.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errPairingState
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(proof[:], p.proofHash) != 1 || p.terminal() {
			return errPairingState
		}
		return nil
	})
	if err != nil {
		pairingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CheckSchema checks the three installed relay tables and the pilot migration.
func CheckSchema(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `
		SELECT p.pilot_scope, f.state_hash, e.goal
		FROM slack_relay_pairings p
		CROSS JOIN slack_relay_oauth_flows f
		CROSS JOIN slack_relay_events e
		LIMIT 0
	`)
	return err
}
