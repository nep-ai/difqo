package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nep-ai/difqo/internal/store"
)

func TestChiefConversationPersistsAndReusesContext(t *testing.T) {
	dsn := os.Getenv("DIFQO_CHIEF_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a fresh migrated PostgreSQL database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	router := http.NewServeMux()
	type input struct {
		history []store.ChatTurn
		current store.ChatTurn
	}
	seen := make(chan input, 2)
	release := make(chan struct{}, 2)
	defer close(release)
	talk := func(ctx context.Context, history []store.ChatTurn, current store.ChatTurn) (string, error) {
		seen <- input{history, current}
		select {
		case <-release:
			return "Tell me what to check.", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	tasks := store.Tasks{DB: db}
	registerChief(router, tasks, talk)
	call := func(method, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/chief/conversation", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	post := func(body string) (int, store.ChatTurn) {
		response := call(http.MethodPost, body)
		var turn store.ChatTurn
		if response.Code == http.StatusAccepted && json.Unmarshal(response.Body.Bytes(), &turn) != nil {
			t.Fatal("invalid turn response")
		}
		return response.Code, turn
	}
	waitFor := func(id int64) store.ChatTurn {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			response := call(http.MethodGet, "")
			var turns []store.ChatTurn
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &turns) != nil {
				t.Fatal(response.Body.String())
			}
			if len(turns) > 0 && turns[0].ID == id && turns[0].State == "completed" {
				return turns[0]
			}
		}
		t.Fatal("turn did not complete")
		return store.ChatTurn{}
	}
	receive := func() input {
		t.Helper()
		select {
		case received := <-seen:
			return received
		case <-time.After(5 * time.Second):
			t.Fatal("Codex conversation was not started")
			return input{}
		}
	}
	firstRequest := `{"request_id":"00000000-0000-4000-8000-000000000001","attachments":[{"name":"fixture.txt","data":"Zml4dHVyZQ=="}]}`
	secondRequest := `{"request_id":"00000000-0000-4000-8000-000000000002","text":"Check the error in that file."}`
	status, first := post(firstRequest)
	if status != http.StatusAccepted || first.State != "running" || len(first.Attachments) != 1 || len(first.Attachments[0].Data) != 0 {
		t.Fatalf("invalid file-only turn: %d %+v", status, first)
	}
	if got := receive(); len(got.history) != 0 || string(got.current.Attachments[0].Data) != "fixture" {
		t.Fatalf("invalid initial Codex input: %+v", got)
	}
	if status, replay := post(firstRequest); status != http.StatusAccepted || replay.ID != first.ID {
		t.Fatalf("unexpected replay: %d %+v", status, replay)
	}
	if status, _ := post(secondRequest); status != http.StatusConflict {
		t.Fatalf("concurrent send returned %d", status)
	}
	release <- struct{}{}
	if reply := waitFor(first.ID); reply.Reply != "Tell me what to check." {
		t.Fatalf("reply not persisted: %+v", reply)
	}
	// A freshly registered handler sees the same conversation and reuses it.
	router = http.NewServeMux()
	registerChief(router, tasks, talk)
	status, second := post(secondRequest)
	if status != http.StatusAccepted {
		t.Fatalf("follow-up returned %d", status)
	}
	got := receive()
	if len(got.history) != 1 || got.history[0].Reply == "" ||
		len(got.current.Attachments) != 1 || string(got.current.Attachments[0].Data) != "fixture" {
		t.Fatalf("follow-up lost context: %+v", got)
	}
	release <- struct{}{}
	waitFor(second.ID)
}
