package slackrelay

import (
	"context"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pairingTTL = 15 * time.Minute

var (
	errPairingAccess = errors.New("invalid pairing or pairing proof")
	errPairingState  = errors.New("pairing or OAuth state is no longer usable")
)

type pairingStore struct {
	db     *pgxpool.Pool
	appID  string
	tokens cipher.AEAD
}

type pairing struct {
	proofHash                          []byte
	expired, revoked, pending, claimed bool
	identity                           identity
	workspace, channel                 string
	sealedToken                        []byte
}

func (p pairing) terminal() bool { return p.expired || p.revoked }

type flow struct {
	pairingID string
	kind      flowKind
	stateHash [sha256.Size]byte
	nonce     string
}

// lock serializes transitions on a pairing across relay instances. No caller
// holds this lock while contacting the provider.
func (s pairingStore) lock(ctx context.Context, tx pgx.Tx, id string) (pairing, error) {
	var p pairing
	err := tx.QueryRow(ctx, `
		WITH locked AS (
			SELECT proof_hash, expires_at, revoked, approval_pending, claimed, verified_workspace,
			       verified_operator, installed_workspace, dm_channel, sealed_bot_token
			FROM slack_relay_pairings
			WHERE app_id = $1 AND id = $2
			FOR UPDATE
		)
		SELECT proof_hash, NOT claimed AND expires_at <= clock_timestamp(), revoked, approval_pending, claimed,
		       verified_workspace, verified_operator, installed_workspace, dm_channel, sealed_bot_token
		FROM locked
	`, s.appID, id).Scan(&p.proofHash, &p.expired, &p.revoked, &p.pending, &p.claimed,
		&p.identity.Workspace, &p.identity.Operator, &p.workspace, &p.channel, &p.sealedToken)
	return p, err
}

func (s pairingStore) owned(ctx context.Context, id, proof string, register bool, use func(pgx.Tx, pairing) error) error {
	hash := sha256.Sum256([]byte(proof))
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if register {
			_, err := tx.Exec(ctx, `
				INSERT INTO slack_relay_pairings (app_id, id, proof_hash, expires_at)
				VALUES ($1, $2, $3, clock_timestamp() + make_interval(secs => $4))
				ON CONFLICT (app_id, id) DO NOTHING
			`, s.appID, id, hash[:], pairingTTL.Seconds())
			if err != nil {
				return err
			}
		}
		p, err := s.lock(ctx, tx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errPairingAccess
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(hash[:], p.proofHash) != 1 {
			return errPairingAccess
		}
		return use(tx, p)
	})
}

func (s pairingStore) read(ctx context.Context, id, proof string) (pairing, error) {
	var result pairing
	err := s.owned(ctx, id, proof, true, func(tx pgx.Tx, p pairing) error {
		if p.expired && !p.revoked {
			if err := s.retire(ctx, tx, id); err != nil {
				return err
			}
		}
		result = p
		return nil
	})
	return result, err
}

func (s pairingStore) start(ctx context.Context, id, proof string, kind flowKind, state, nonce string) (string, error) {
	var team string
	err := s.owned(ctx, id, proof, false, func(tx pgx.Tx, p pairing) error {
		if p.terminal() || (kind == signIn && p.identity.Workspace != "") || (kind == installApp && p.workspace != "") {
			return errPairingState
		}
		team = p.identity.Workspace
		if kind == signIn {
			team = p.workspace
		}
		hash := sha256.Sum256([]byte(state))
		_, err := tx.Exec(ctx, `
			INSERT INTO slack_relay_oauth_flows (app_id, pairing_id, purpose, state_hash, nonce)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (app_id, pairing_id, purpose) DO UPDATE
			SET state_hash = EXCLUDED.state_hash, nonce = EXCLUDED.nonce, consumed = false
		`, s.appID, id, kind, hash[:], nonce)
		return err
	})
	return team, err
}

func (s pairingStore) pending(ctx context.Context, id, proof string) error {
	return s.owned(ctx, id, proof, false, func(tx pgx.Tx, p pairing) error {
		if p.terminal() || p.identity.Workspace == "" || p.workspace != "" {
			return errPairingState
		}
		tag, err := tx.Exec(ctx, `
			UPDATE slack_relay_pairings p SET approval_pending = true
			WHERE app_id = $1 AND id = $2 AND EXISTS (
				SELECT 1 FROM slack_relay_oauth_flows f
				WHERE f.app_id = p.app_id AND f.pairing_id = p.id AND f.purpose = 'install'
			)
		`, s.appID, id)
		if err == nil && tag.RowsAffected() == 0 {
			return errPairingState
		}
		return err
	})
}

func (s pairingStore) consume(ctx context.Context, kind flowKind, state string) (flow, error) {
	f := flow{kind: kind, stateHash: sha256.Sum256([]byte(state))}
	var retired bool
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT pairing_id FROM slack_relay_oauth_flows
			WHERE app_id = $1 AND purpose = $2 AND state_hash = $3
		`, s.appID, kind, f.stateHash[:]).Scan(&f.pairingID)
		if err != nil {
			return err
		}
		p, err := s.lock(ctx, tx, f.pairingID)
		if err != nil {
			return err
		}
		if p.revoked {
			return errPairingState
		}
		if p.expired {
			retired = true
			return s.retire(ctx, tx, f.pairingID)
		}
		return tx.QueryRow(ctx, `
			UPDATE slack_relay_oauth_flows SET consumed = true
			WHERE app_id = $1 AND pairing_id = $2 AND purpose = $3 AND state_hash = $4 AND NOT consumed
			RETURNING nonce
		`, s.appID, f.pairingID, kind, f.stateHash[:]).Scan(&f.nonce)
	})
	// Return a conflict only after any retirement has committed.
	if (err == nil && retired) || errors.Is(err, pgx.ErrNoRows) {
		err = errPairingState
	}
	return f, err
}

func (s pairingStore) complete(ctx context.Context, f flow, who identity, installed installation) error {
	var retired bool
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		p, err := s.lock(ctx, tx, f.pairingID)
		if err != nil {
			return err
		}
		if p.revoked {
			return errPairingState
		}
		if p.expired {
			retired = true
			return s.retire(ctx, tx, f.pairingID)
		}
		var current bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM slack_relay_oauth_flows
				WHERE app_id = $1 AND pairing_id = $2 AND purpose = $3 AND state_hash = $4 AND consumed
			)
		`, s.appID, f.pairingID, f.kind, f.stateHash[:]).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			return errPairingState
		}
		if f.kind == signIn {
			p.identity = who
			if p.workspace != who.Workspace {
				p.workspace, p.sealedToken = "", nil
			}
		} else {
			if p.identity.Workspace != "" && p.identity.Workspace != installed.workspace {
				return errPairingState
			}
			p.workspace, p.pending = installed.workspace, false
			p.sealedToken = s.tokens.Seal(nil, nil, []byte(installed.botToken), s.tokenScope(f.pairingID, p.workspace))
		}
		tag, err := tx.Exec(ctx, `
			UPDATE slack_relay_pairings
			SET verified_workspace = $3, verified_operator = $4, installed_workspace = $5,
			    sealed_bot_token = $6, approval_pending = $7
			WHERE app_id = $1 AND id = $2 AND expires_at > clock_timestamp()
		`, s.appID, f.pairingID, p.identity.Workspace, p.identity.Operator, p.workspace, p.sealedToken, p.pending)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			retired = true
			return s.retire(ctx, tx, f.pairingID)
		}
		return nil
	})
	if err == nil && retired {
		return errPairingState
	}
	return err
}

func (s pairingStore) tokenScope(id, workspace string) []byte {
	return []byte(s.appID + ":" + id + ":" + workspace)
}

func (s pairingStore) finishDM(ctx context.Context, id, proof string, channel string, revoked bool) error {
	return s.owned(ctx, id, proof, false, func(tx pgx.Tx, p pairing) error {
		if p.terminal() {
			return nil
		}
		if revoked {
			return s.retire(ctx, tx, id)
		}
		if p.channel != "" {
			return nil
		}
		_, err := tx.Exec(ctx, `
			UPDATE slack_relay_pairings SET dm_channel = $3, sealed_bot_token = NULL
			WHERE app_id = $1 AND id = $2
		`, s.appID, id, channel)
		return err
	})
}

func (s pairingStore) revoke(ctx context.Context, id, proof string) error {
	return s.owned(ctx, id, proof, false, func(tx pgx.Tx, p pairing) error {
		if p.revoked {
			return nil
		}
		return s.retire(ctx, tx, id)
	})
}

// Retain a tombstone so PUT cannot re-register a retired pairing ID.
func (s pairingStore) retire(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `
		UPDATE slack_relay_pairings SET revoked = true, approval_pending = false, claimed = false, verified_workspace = '',
		    verified_operator = '', installed_workspace = '', dm_channel = '', sealed_bot_token = NULL
		WHERE app_id = $1 AND id = $2
	`, s.appID, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "DELETE FROM slack_relay_oauth_flows WHERE app_id = $1 AND pairing_id = $2", s.appID, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE slack_relay_events SET goal = NULL WHERE app_id = $1 AND pairing_id = $2 AND goal IS NOT NULL
	`, s.appID, id)
	return err
}
