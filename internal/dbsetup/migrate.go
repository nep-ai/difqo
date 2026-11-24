package dbsetup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var migrationName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	name, digest, body string
}

func loadMigrations(files fs.FS) ([]migration, error) {
	entries, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("no SQL migration files found")
	}
	migrations := make([]migration, 0, len(entries))
	for i, name := range entries {
		match := migrationName.FindStringSubmatch(name)
		if match == nil || match[1] != fmt.Sprintf("%04d", i+1) {
			return nil, fmt.Errorf("migration sequence is invalid at %q", name)
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("could not read migration %s", name)
		}
		// Normalize line endings so source checkouts and release bundles agree.
		source := strings.ReplaceAll(string(data), "\r\n", "\n")
		body, begin := strings.CutPrefix(strings.TrimSpace(source), "BEGIN;\n")
		body, commit := strings.CutSuffix(body, "\nCOMMIT;")
		if !begin || !commit || strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("migration %s must have an outer BEGIN/COMMIT wrapper", name)
		}
		migrations = append(migrations, migration{name: name, digest: fmt.Sprintf("%x", sha256.Sum256([]byte(source))), body: body})
	}
	return migrations, nil
}

// Migrate applies all pending migrations and their history in one transaction.
func Migrate(ctx context.Context, db *sql.DB, files fs.FS, baseline string) (int, error) {
	migrations, err := loadMigrations(files)
	if err != nil {
		return 0, err
	}
	through := 0
	if baseline != "" {
		index := slices.IndexFunc(migrations, func(m migration) bool { return m.name == baseline })
		if index < 0 {
			return 0, errors.New("baseline must name a migration from the supplied bundle")
		}
		through = index + 1
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, databaseError("begin database setup", err)
	}
	defer tx.Rollback()
	// A transaction-scoped lock serializes competing Difqo setup commands.
	if _, err := tx.ExecContext(ctx, "SELECT pg_catalog.pg_advisory_xact_lock(43166926713)"); err != nil {
		return 0, databaseError("lock database for setup", err)
	}

	var schema sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT pg_catalog.current_schema()").Scan(&schema); err != nil {
		return 0, databaseError("inspect database schema", err)
	}
	if schema.String != "public" {
		return 0, errors.New("database setup requires public as the current schema")
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL search_path = public"); err != nil {
		return 0, databaseError("select public schema", err)
	}
	applied, err := prepareHistory(ctx, tx, migrations, through)
	if err != nil {
		return 0, err
	}
	for _, m := range migrations[applied:] {
		// The original files retain their psql wrappers. Executing the bodies
		// inside a DO block in this transaction prevents an inner COMMIT or
		// ROLLBACK from changing the transaction boundary.
		if _, err := tx.ExecContext(ctx, "SELECT pg_catalog.set_config('difqo.migration_sql', $1, true)", m.body); err != nil {
			return 0, databaseError("prepare "+m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `DO $difqo$ BEGIN
			EXECUTE pg_catalog.current_setting('difqo.migration_sql');
		END $difqo$`); err != nil {
			return 0, databaseError("apply "+m.name, err)
		}
		if err := record(ctx, tx, m); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, databaseError("commit database setup", err)
	}
	return len(migrations) - applied, nil
}

func prepareHistory(ctx context.Context, tx *sql.Tx, migrations []migration, through int) (int, error) {
	var tracked, populated, legacy bool
	err := tx.QueryRowContext(ctx, `
		SELECT pg_catalog.to_regclass('public.difqo_schema_migrations') IS NOT NULL,
			EXISTS (SELECT 1 FROM pg_catalog.pg_class c
				JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public'),
			pg_catalog.to_regclass('public.tasks') IS NOT NULL
				AND pg_catalog.to_regclass('public.attempts') IS NOT NULL`).Scan(&tracked, &populated, &legacy)
	if err != nil {
		return 0, databaseError("inspect migration history", err)
	}
	if tracked {
		count, err := readHistory(ctx, tx, migrations)
		if err == nil && through > count {
			return 0, errors.New("baseline cannot advance existing migration history")
		}
		return count, err
	}
	if populated && (!legacy || through == 0) {
		return 0, errors.New("public schema is not empty and has no Difqo migration history; use an empty database or confirm the last manually applied Difqo migration with -baseline")
	}
	if !populated && through != 0 {
		return 0, errors.New("baseline requires an existing Difqo schema")
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE public.difqo_schema_migrations (
		name text PRIMARY KEY,
		sha256 text NOT NULL,
		recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
	)`); err != nil {
		return 0, databaseError("create migration history", err)
	}
	for _, m := range migrations[:through] {
		if err := record(ctx, tx, m); err != nil {
			return 0, err
		}
	}
	return through, nil
}

func readHistory(ctx context.Context, tx *sql.Tx, migrations []migration) (int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name, sha256 FROM public.difqo_schema_migrations ORDER BY name")
	if err != nil {
		return 0, databaseError("read migration history", err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var name, digest string
		if err := rows.Scan(&name, &digest); err != nil {
			return 0, databaseError("read migration history", err)
		}
		if i >= len(migrations) || name != migrations[i].name || digest != migrations[i].digest {
			return 0, errors.New("recorded migration history does not match this bundle; use the matching or newer release")
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return 0, databaseError("read migration history", err)
	}
	return i, nil
}

func record(ctx context.Context, tx *sql.Tx, m migration) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO public.difqo_schema_migrations (name, sha256) VALUES ($1, $2)", m.name, m.digest); err != nil {
		return databaseError("record "+m.name, err)
	}
	return nil
}

func databaseError(action string, err error) error {
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) {
		return fmt.Errorf("%s failed (PostgreSQL SQLSTATE %s)", action, strconv.QuoteToASCII(postgres.Code))
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	return fmt.Errorf("%s failed", action)
}
