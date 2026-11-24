package slackrelay

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nep-ai/difqo/internal/slackintake"
	"github.com/nep-ai/difqo/internal/store"
)

func TestOfficialSlackDelivery(t *testing.T) {
	dsn := os.Getenv("DIFQO_SLACK_DELIVERY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set DIFQO_SLACK_DELIVERY_TEST_DATABASE_URL to a migrated disposable local database")
	}
	f := newTestFixture(t)
	ctx := t.Context()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tasks := store.Tasks{DB: db}
	// Reset only the singleton in the dedicated CI fixture for repeated runs.
	if _, err := db.ExecContext(ctx, "UPDATE slack_connection SET mode = 'disconnected', generation = generation + 1, pairing = NULL"); err != nil {
		t.Fatal(err)
	}
	decision := f.appID + " fixture decision"
	if _, err := db.ExecContext(ctx, "INSERT INTO project_decisions (body) VALUES ($1)", decision); err != nil {
		t.Fatal(err)
	}

	event := f.fixtureEvent()
	event.Event.Text = "goal " + f.appID + " committed once"
	late := f.fixtureEvent()
	late.Event.Text = "goal " + f.appID + " must not be imported"
	firstReceipt, lateRead, releaseRead := make(chan struct{}), make(chan struct{}), make(chan struct{})
	firstClaim, firstDelete := true, true
	var mu sync.Mutex
	f.wrapRelay = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			if strings.HasSuffix(r.URL.Path, "/claim") && firstClaim {
				firstClaim = false
				mu.Unlock()
				// Commit the claim and simulate a lost success response.
				reply := httptest.NewRecorder()
				next.ServeHTTP(reply, r)
				if reply.Code != http.StatusNoContent {
					t.Errorf("claim returned %d", reply.Code)
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if r.Method == http.MethodDelete && firstDelete {
				firstDelete = false
				mu.Unlock()
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			mu.Unlock()

			if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/events/"+event.EventID) {
				var count int
				if err := db.QueryRowContext(r.Context(), `
					SELECT count(*) FROM slack_task_sources s JOIN tasks t ON s.task_id = t.id
					JOIN project_decisions d ON d.id = t.decision_id
					WHERE s.app_id = $1 AND s.event_id = $2 AND t.goal = $3 AND d.body = $4
				`, f.appID, event.EventID, f.appID+" committed once", decision).Scan(&count); err != nil || count != 1 {
					t.Errorf("receipt before durable task and decision: count=%d err=%v", count, err)
				}
				close(firstReceipt)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events") {
				reply := httptest.NewRecorder()
				next.ServeHTTP(reply, r)
				if strings.Contains(reply.Body.String(), late.EventID) {
					close(lateRead)
					<-releaseRead
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(reply.Code)
				w.Write(reply.Body.Bytes())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	wrap := f.wrapRelay
	f.restart()
	connections, err := slackintake.NewFixtureConnections(tasks, f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	view, err := connections.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.id = view.PairingID
	state, err := tasks.SlackConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.proof = state.Pairing.Proof
	binding := f.prepareBinding()
	if _, err := connections.Refresh(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	path := "/pairings/" + f.id + "/events"
	f.request("GET", path, f.proof, nil, http.StatusConflict, nil)
	if v, err := connections.Confirm(ctx, f.id, binding); !errors.Is(err, slackintake.ErrRelayUnavailable) || v.State != "claiming" {
		t.Fatalf("lost claim response: state=%q err=%v", v.State, err)
	}
	view, err = connections.Confirm(ctx, f.id, binding)
	if err != nil || view.Mode != "official" {
		t.Fatalf("claim retry: state=%q err=%v", view.State, err)
	}
	f.request("GET", path, randomSecret(), nil, http.StatusUnauthorized, nil)
	f.request("PUT", path+"/"+late.EventID, randomSecret(), nil, http.StatusUnauthorized, nil)
	f.request("PUT", path+"/"+late.EventID, f.proof, nil, http.StatusConflict, nil)
	f.webhook(event, http.StatusOK)
	stop := runDelivery(ctx, connections)
	defer stop()
	awaitDelivery(t, firstReceipt)
	stop()
	if f.queueCount(f.id) != 1 {
		t.Fatal("lost receipt removed queued goal")
	}
	f.wrapRelay = nil
	f.restart()
	connections, err = slackintake.NewFixtureConnections(tasks, f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	stopReplay := runDelivery(ctx, connections)
	defer stopReplay()
	waitDelivery(t, func() bool { return f.queueCount(f.id) == 0 })
	stopReplay()
	f.request("PUT", path+"/"+event.EventID, f.proof, nil, http.StatusNoContent, nil)
	f.webhook(event, http.StatusOK)
	if f.queueCount(f.id) != 0 || f.queueCount("") != 1 {
		t.Fatal("receipt discarded the durable replay identity")
	}

	f.wrapRelay = wrap
	f.restart()
	f.webhook(late, http.StatusOK)
	stopLate := runDelivery(ctx, connections)
	defer stopLate()
	var release sync.Once
	defer release.Do(func() { close(releaseRead) })
	awaitDelivery(t, lateRead)
	if v, err := connections.Disconnect(ctx, view.Generation, f.id); !errors.Is(err, slackintake.ErrRelayUnavailable) || v.State != "disconnecting" {
		t.Fatalf("relay outage during disconnect: state=%q err=%v", v.State, err)
	}
	release.Do(func() { close(releaseRead) })
	waitDelivery(t, func() bool {
		v, err := connections.Status(ctx)
		return err == nil && v.State == "disconnected"
	})
	stopLate()
	f.request("GET", path, f.proof, nil, http.StatusConflict, nil)
	f.request("PUT", path+"/"+late.EventID, f.proof, nil, http.StatusConflict, nil)
	if f.queueCount(f.id) != 0 {
		t.Fatal("disconnect retained queued goals")
	}
	var sources int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM slack_task_sources WHERE app_id = $1", f.appID).Scan(&sources); err != nil || sources != 1 {
		t.Fatalf("replay or revoked delivery created an extra task: count=%d err=%v", sources, err)
	}
}

func runDelivery(ctx context.Context, c *slackintake.Connections) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	return func() { cancel(); <-done }
}

func awaitDelivery(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not reach the expected relay request")
	}
}

func waitDelivery(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("delivery state did not converge")
}
