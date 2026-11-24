package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nep-ai/difqo/internal/githubconnection"
	"github.com/nep-ai/difqo/internal/githubintake"
	"github.com/nep-ai/difqo/internal/store"
)

func runGitHubIntake(args []string) error {
	flags := flag.NewFlagSet("github-intake", flag.ContinueOnError)
	var config githubintake.Config
	flags.StringVar(&config.Repository, "repository", "", "enrolled GitHub repository in owner/name form")
	flags.Int64Var(&config.Enrollment.RepositoryID, "repository-id", 0, "numeric ID of the enrolled repository")
	flags.Int64Var(&config.Enrollment.OwnerID, "owner-id", 0, "numeric GitHub user ID of the linked local owner")
	flags.StringVar(&config.Handle, "handle", "difqo", "app mention handle without @")
	since := flags.String("since", "", "initial enrollment time in RFC3339, reused on subsequent polls")
	origin := flags.String("connections", "", "Connections HTTPS origin for the official GitHub pilot")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	start, err := time.Parse(time.RFC3339, *since)
	if err != nil || start.Nanosecond() != 0 || flags.NArg() != 0 {
		return errors.New("github-intake requires -since in RFC3339 at second precision and no positional arguments")
	}
	config.Enrollment.Start = start
	config.Token = os.Getenv("DIFQO_GITHUB_TOKEN")
	dsn := os.Getenv("DIFQO_DATABASE_URL")
	if dsn == "" {
		return errors.New("github-intake requires DIFQO_DATABASE_URL")
	}
	if *origin == "" && config.Token == "" {
		return errors.New("github-intake requires -connections for the official app, or DIFQO_GITHUB_TOKEN for a pre-issued token")
	}
	if *origin != "" && (config.Token != "" || config.Repository != "" ||
		config.Enrollment.RepositoryID != 0 || config.Enrollment.OwnerID != 0) {
		return errors.New("official GitHub intake gets the repository and owner from Connections; clear manual token and identity settings")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid database connection configuration")
	}
	defer db.Close()
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 55*time.Second)
	defer cancel()
	if *origin != "" {
		grant, err := githubconnection.Fetch(ctx, *origin, os.Getenv("DIFQO_GITHUB_CONNECTION_PROOF"))
		if err != nil {
			return err
		}
		config.Token = grant.Token
		config.Repository = grant.Repository
		config.Enrollment.RepositoryID = grant.RepositoryID
		config.Enrollment.OwnerID = grant.OwnerID
	}
	count, err := githubintake.Poll(ctx, config, store.Tasks{DB: db})
	if err != nil {
		return err
	}
	fmt.Printf("Imported %d GitHub task(s)\n", count)
	return nil
}
