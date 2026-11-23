package githubintake

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/nep-ai/difqo/internal/controlplane"
	"github.com/nep-ai/difqo/internal/store"
)

var fixtureStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func fixtureConfig() Config {
	return Config{
		Enrollment: store.GitHubEnrollment{RepositoryID: 42, OwnerID: 17, Start: fixtureStart},
		Repository: "fixture/repo", Handle: "difqo", Token: "synthetic-installation-token",
	}
}

func fixtureGitHub(t *testing.T, mode *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer synthetic-installation-token" {
			t.Error("expected a read-only authenticated request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/installation/repositories":
			id := 42
			if mode.Load() == 1 {
				id = 43
			}
			fmt.Fprintf(w, `{"repositories":[{"id":%d,"full_name":"fixture/repo"}]}`, id)
		case "/repos/fixture/repo/issues/comments":
			if mode.Load() == 1 {
				t.Error("read comments outside installation scope")
			}
			if mode.Load() == 2 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if mode.Load() == 3 {
				w.Header().Set("Link", `<https://example.invalid/page/2>; rel="next"`)
			}
			if r.URL.Query().Get("sort") != "updated" || r.URL.Query().Get("per_page") != "100" {
				t.Error("unexpected comment query")
			}
			since, err := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
			if err != nil {
				t.Error("missing comment cursor")
			}
			rows := []comment{}
			for i, row := range []struct {
				owner int64
				body  string
				age   time.Duration
			}{
				{17, "@Difqo Repair the fixture greeting", time.Second},
				{18, "@difqo Unauthorized goal", time.Second},
				{17, "@difqo-extra Wrong app", time.Second},
				{17, "@difqo Old comment", -time.Second},
				{17, "@difqo Bot comment", time.Second},
			} {
				item := comment{
					ID: int64(i + 1), IssueURL: "http://" + r.Host + "/repos/fixture/repo/issues/34", Body: row.body,
					CreatedAt: fixtureStart.Add(row.age), UpdatedAt: fixtureStart.Add(time.Second),
				}
				item.User.ID, item.User.Type = row.owner, "User"
				if i == 4 {
					item.User.Type = "Bot"
				}
				if item.UpdatedAt.After(since) {
					rows = append(rows, item)
				}
			}
			if mode.Load() == 4 {
				rows[0].IssueURL = "http://" + r.Host + "/repos/fixture/other/issues/34"
			}
			_ = json.NewEncoder(w).Encode(rows)
		default:
			t.Errorf("unexpected GitHub path %q", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

type recordedStore struct {
	cursor   time.Time
	accepted []store.GitHubTask
	writes   int
}

func (s *recordedStore) GitHubCursor(context.Context, store.GitHubEnrollment) (time.Time, error) {
	return s.cursor, nil
}

func (s *recordedStore) ImportGitHub(_ context.Context, _ store.GitHubEnrollment, through time.Time, tasks []store.GitHubTask) (int, error) {
	s.cursor, s.accepted = through, tasks
	s.writes++
	return len(tasks), nil
}

func TestPollAuthenticatesBeforeImport(t *testing.T) {
	for mode, name := range []string{"enrolled owner", "wrong repository", "revoked token", "extra page", "wrong thread"} {
		t.Run(name, func(t *testing.T) {
			var fixtureMode atomic.Int32
			fixtureMode.Store(int32(mode))
			api := fixtureGitHub(t, &fixtureMode)
			tasks := &recordedStore{cursor: fixtureStart}
			count, err := poll(t.Context(), api.Client(), api.URL, fixtureConfig(), tasks)
			if mode == 0 {
				want := store.GitHubTask{CommentID: 1, ThreadNumber: 34, Goal: "Repair the fixture greeting"}
				if err != nil || count != 1 || len(tasks.accepted) != 1 || tasks.accepted[0] != want ||
					!tasks.cursor.Equal(fixtureStart.Add(time.Second)) || tasks.writes != 1 {
					t.Fatalf("unexpected import: %d, %v, %+v", count, err, tasks)
				}
				return
			}
			if err == nil || count != 0 || tasks.writes != 0 || !tasks.cursor.Equal(fixtureStart) {
				t.Fatalf("failed poll mutated state: %d, %v, %+v", count, err, tasks)
			}
		})
	}
}

func TestGitHubPostgres(t *testing.T) {
	dsn := os.Getenv("DIFQO_GITHUB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set DIFQO_GITHUB_TEST_DATABASE_URL for the PostgreSQL contract test")
	}
	ctx := t.Context()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("could not configure test database")
	}
	defer admin.Close()
	schema := fmt.Sprintf("difqo_github_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("could not configure test schema")
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	migrations, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatal("could not locate migrations")
	}
	for _, migration := range migrations {
		sql, err := os.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", migration, err)
		}
	}

	tasks := store.Tasks{DB: db}
	decision, err := tasks.SaveDecision(ctx, "Use the current project decision")
	if err != nil {
		t.Fatal(err)
	}
	var mode atomic.Int32
	api := fixtureGitHub(t, &mode)
	config := fixtureConfig()
	var wg sync.WaitGroup
	var total atomic.Int32
	for range 4 {
		wg.Go(func() {
			count, err := poll(ctx, api.Client(), api.URL, config, tasks)
			if err != nil {
				t.Error(err)
				return
			}
			total.Add(int32(count))
		})
	}
	wg.Wait()
	if total.Load() != 1 {
		t.Fatalf("concurrent replays imported %d tasks", total.Load())
	}

	// A subsequent poll intentionally re-reads the cursor's last second.
	count, err := poll(ctx, api.Client(), api.URL, config, store.Tasks{DB: db})
	if err != nil || count != 0 {
		t.Fatalf("replay imported %d tasks: %v", count, err)
	}
	changed := config
	changed.Enrollment.OwnerID++
	if _, err := poll(ctx, api.Client(), api.URL, changed, tasks); !errors.Is(err, store.ErrGitHubEnrollment) {
		t.Fatalf("replacing the linked owner returned %v", err)
	}
	handler := controlplane.Handler(tasks, nil, nil, "", nil, nil)
	claim := func(body string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/tasks/claim", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(res, req)
		return res
	}
	for _, body := range []string{`{}`, `{"github_repository_id":43}`} {
		if result := claim(body); result.Code != http.StatusNoContent {
			t.Fatalf("wrong repository scope %s returned %d: %s", body, result.Code, result.Body)
		}
	}
	result := claim(`{"github_repository_id":42}`)
	var task store.Task
	if result.Code != http.StatusOK || json.Unmarshal(result.Body.Bytes(), &task) != nil ||
		task.Goal != "Repair the fixture greeting" || task.Decision == nil || task.Decision.ID != decision.ID {
		t.Fatalf("scoped claim returned %d: %s", result.Code, result.Body)
	}
	var source store.GitHubTask
	var author int64
	if err := db.QueryRowContext(ctx, `
		SELECT comment_id, thread_number, author_id FROM github_task_sources WHERE task_id = $1`, task.ID).
		Scan(&source.CommentID, &source.ThreadNumber, &author); err != nil ||
		source.CommentID != 1 || source.ThreadNumber != 34 || author != config.Enrollment.OwnerID {
		t.Fatalf("unexpected saved source: %+v, author %d, %v", source, author, err)
	}
}
