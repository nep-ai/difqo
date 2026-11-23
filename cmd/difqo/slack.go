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

func runSlack(args []string, socketMode bool) error {
	name := "slack-import"
	if socketMode {
		name = "slack-socket"
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	var binding store.SlackBinding
	flags.StringVar(&binding.AppID, "app-id", "", "linked Slack app ID")
	flags.StringVar(&binding.WorkspaceID, "workspace-id", "", "linked Slack workspace ID")
	flags.StringVar(&binding.OperatorID, "operator-id", "", "linked local operator's Slack user ID")
	flags.StringVar(&binding.ChannelID, "dm-id", "", "private DM conversation ID")
	flags.Usage = func() {
		if socketMode {
			fmt.Fprintln(flags.Output(), "Receive goals from one linked DM through a private Slack app in Socket Mode.")
		} else {
			fmt.Fprintln(flags.Output(), "Import one trusted normalized Slack event from stdin. This command is an offline intake harness.")
		}
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s takes no positional arguments", name)
	}
	dsn := os.Getenv("DIFQO_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("%s requires DIFQO_DATABASE_URL", name)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid database connection configuration")
	}
	defer db.Close()
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tasks := store.Tasks{DB: db}
	if socketMode {
		err := slackintake.RunSocketMode(signalCtx, tasks, binding, os.Getenv("DIFQO_SLACK_APP_TOKEN"))
		if errors.Is(err, context.Canceled) && signalCtx.Err() != nil {
			return nil
		}
		return err
	}
	ctx, cancel := context.WithTimeout(signalCtx, 10*time.Second)
	defer cancel()
	created, err := slackintake.Import(ctx, tasks, binding, os.Stdin)
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
