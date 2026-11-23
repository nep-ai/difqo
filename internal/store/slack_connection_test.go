package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPrivateSlackSessionGeneration(t *testing.T) {
	dsn := os.Getenv("DIFQO_SLACK_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set DIFQO_SLACK_TEST_DATABASE_URL to exercise PostgreSQL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid Slack test database configuration")
	}
	ctx := t.Context()
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := fmt.Sprintf("difqo_slack_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	migrations, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatal("could not locate migrations")
	}
	for _, migration := range migrations {
		script, err := os.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("%s: %v", migration, err)
		}
	}

	tasks := Tasks{DB: db}
	binding := SlackBinding{AppID: "A11", WorkspaceID: "T11", OperatorID: "U11", ChannelID: "D11"}
	old, err := tasks.CheckSlackBinding(ctx, binding)
	if err != nil || old <= 0 {
		t.Fatalf("capture private connector generation: %d, %v", old, err)
	}
	created, err := tasks.ImportPrivateSlack(ctx, binding, "EvBefore", "First goal", old)
	if err != nil || !created {
		t.Fatalf("initial import: created=%v, err=%v", created, err)
	}
	for _, mode := range []string{"disconnected", "private"} {
		if _, err := tasks.UpdateSlackConnection(ctx, func(state *SlackConnection) error {
			state.Mode = mode
			state.Generation++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	current, err := tasks.CheckSlackBinding(ctx, binding)
	if err != nil || current <= old {
		t.Fatalf("capture replacement generation: %d, %v", current, err)
	}
	created, err = tasks.ImportPrivateSlack(ctx, binding, "EvAfter", "Second goal", old)
	if created || !errors.Is(err, ErrSlackBinding) {
		t.Fatalf("old connector accepted: created=%v, err=%v", created, err)
	}
	created, err = tasks.ImportPrivateSlack(ctx, binding, "EvAfter", "Second goal", current)
	if err != nil || !created {
		t.Fatalf("replacement connector did not create task: created=%v, err=%v", created, err)
	}
}
