package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/nep-ai/difqo/internal/dbsetup"
)

func runDatabaseSetup(args []string) error {
	flags := flag.NewFlagSet("setup-database", flag.ContinueOnError)
	migrations := flags.String("migrations", "", "directory of trusted SQL migrations (default: ../migrations relative to the executable)")
	baseline := flags.String("baseline", "", "last migration already applied using psql to an untracked Difqo database")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("setup-database accepts flags only; see -help")
	}
	dsn := os.Getenv("DIFQO_DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		return errors.New("set DIFQO_DATABASE_URL for an existing PostgreSQL database before running setup-database")
	}
	if *migrations == "" {
		executable, err := os.Executable()
		if err != nil {
			return errors.New("could not locate the executable; supply -migrations")
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return errors.New("could not resolve the executable; supply -migrations")
		}
		*migrations = filepath.Join(filepath.Dir(executable), "..", "migrations")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid PostgreSQL connection configuration; check DIFQO_DATABASE_URL")
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		return errors.New("could not connect to PostgreSQL; check that the database is running and DIFQO_DATABASE_URL is correct")
	}
	setupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	count, err := dbsetup.Migrate(setupCtx, db, os.DirFS(*migrations), *baseline)
	if err != nil {
		return err
	}
	fmt.Printf("PostgreSQL is ready. Applied %d migration(s).\n", count)
	return nil
}
