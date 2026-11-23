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

	"github.com/nep-ai/difqo/internal/slackintake"
	"github.com/nep-ai/difqo/internal/store"
)

func runSlackImport(args []string) error {
	flags := flag.NewFlagSet("slack-import", flag.ContinueOnError)
	var binding store.SlackBinding
	flags.StringVar(&binding.AppID, "app-id", "", "linked Slack app ID")
	flags.StringVar(&binding.WorkspaceID, "workspace-id", "", "linked Slack workspace ID")
	flags.StringVar(&binding.OperatorID, "operator-id", "", "linked local operator's Slack user ID")
	flags.StringVar(&binding.ChannelID, "dm-id", "", "private DM conversation ID")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Import one trusted normalized Slack event from stdin. This command is an offline intake harness.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("slack-import takes its event from stdin")
	}
	dsn := os.Getenv("DIFQO_DATABASE_URL")
	if dsn == "" {
		return errors.New("slack-import requires DIFQO_DATABASE_URL")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid database connection configuration")
	}
	defer db.Close()
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 10*time.Second)
	defer cancel()
	created, err := slackintake.Import(ctx, store.Tasks{DB: db}, binding, os.Stdin)
	if err != nil {
		if errors.Is(err, store.ErrSlackBinding) || errors.Is(err, store.ErrSlackReplay) {
			return err
		}
		return fmt.Errorf("Slack import rejected: %w", err)
	}
	if created {
		fmt.Println("Imported one Slack goal")
	} else {
		fmt.Println("Slack event already imported")
	}
	return nil
}
