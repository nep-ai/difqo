// Package migrations applies the hosted relay schema.
package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed *.sql
var scripts embed.FS

func Apply(ctx context.Context, dsn string) error {
	if dsn == "" {
		return errors.New("DIFQO_RELAY_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("could not connect to the relay database")
	}
	defer db.Close(context.Background())
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			SELECT pg_advisory_xact_lock(45190606043);
			CREATE TABLE IF NOT EXISTS difqo_relay_migrations (
				name text PRIMARY KEY,
				checksum text NOT NULL
			)
		`); err != nil {
			return errors.New("could not lock or initialize the relay migration ledger")
		}
		entries, err := scripts.ReadDir(".")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			raw, err := scripts.ReadFile(name)
			if err != nil {
				return err
			}
			if err := apply(ctx, tx, name, string(raw)); err != nil {
				return err
			}
		}
		return nil
	})
}

func apply(ctx context.Context, tx pgx.Tx, name, raw string) error {
	sum := sha256.Sum256([]byte(raw))
	checksum := hex.EncodeToString(sum[:])
	var saved string
	err := tx.QueryRow(ctx, "SELECT checksum FROM difqo_relay_migrations WHERE name = $1", name).Scan(&saved)
	switch {
	case err == nil:
		if saved != checksum {
			return fmt.Errorf("relay migration %s differs from its recorded checksum", name)
		}
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("could not read relay migration %s", name)
	}
	sql, begins := strings.CutPrefix(strings.TrimSpace(raw), "BEGIN;")
	sql, ends := strings.CutSuffix(sql, "COMMIT;")
	if !begins || !ends {
		return fmt.Errorf("relay migration %s must have a BEGIN/COMMIT wrapper", name)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("could not apply relay migration %s", name)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO difqo_relay_migrations VALUES ($1, $2)", name, checksum); err != nil {
		return fmt.Errorf("could not record relay migration %s", name)
	}
	return nil
}
