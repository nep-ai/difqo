package slackintake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/nep-ai/difqo/internal/store"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func socketFixture(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Transport: fixtureTransport(func(request *http.Request) (*http.Response, error) {
			host := request.URL.Host
			if host != "slack.com" && host != "wss-fixture.slack.com" {
				return nil, errors.New("unexpected HTTP fixture destination")
			}
			local := request.Clone(request.Context())
			local.URL.Scheme = target.Scheme
			local.URL.Host = target.Host
			return server.Client().Transport.RoundTrip(local)
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var fixtureBinding = store.SlackBinding{AppID: "A11", WorkspaceID: "T11", OperatorID: "U11", ChannelID: "D11"}

func fixtureCallback(eventID string) slackCallback {
	var callback slackCallback
	callback.Type = "event_callback"
	callback.EventID = eventID
	callback.AppID = fixtureBinding.AppID
	callback.WorkspaceID = fixtureBinding.WorkspaceID
	callback.Event.Type = "message"
	callback.Event.Channel = fixtureBinding.ChannelID
	callback.Event.ChannelType = "im"
	callback.Event.User = fixtureBinding.OperatorID
	callback.Event.Text = "goal Review a synthetic snapshot"
	return callback
}

func TestSocketGoalRejectsUnlinkedEvents(t *testing.T) {
	for name, change := range map[string]func(*slackCallback){
		"other app":       func(c *slackCallback) { c.AppID = "A99" },
		"other workspace": func(c *slackCallback) { c.WorkspaceID = "T99" },
		"other sender":    func(c *slackCallback) { c.Event.User = "U99" },
		"other DM":        func(c *slackCallback) { c.Event.Channel = "D99" },
		"group DM":        func(c *slackCallback) { c.Event.ChannelType = "mpim" },
		"shared channel":  func(c *slackCallback) { c.ExternalShare = true },
		"foreign sender":  func(c *slackCallback) { c.Event.UserTeam = "T99" },
		"foreign message": func(c *slackCallback) { c.Event.Team = "T99" },
		"bot":             func(c *slackCallback) { c.Event.BotID = "B99" },
		"app message":     func(c *slackCallback) { c.Event.AppID = "A99" },
		"edited message":  func(c *slackCallback) { c.Event.Subtype = "message_changed" },
		"approval":        func(c *slackCallback) { c.Event.Text = "approve 1" },
	} {
		t.Run(name, func(t *testing.T) {
			callback := fixtureCallback("EvCaseSensitive1")
			change(&callback)
			raw, err := json.Marshal(callback)
			if err != nil {
				t.Fatal(err)
			}
			if id, goal := socketGoal(fixtureBinding, raw); id != "" || goal != "" {
				t.Fatal("event should have been rejected")
			}
		})
	}
}

func TestSocketModeACKAndReconnect(t *testing.T) {
	var opened, connections, persisted atomic.Int32
	client := socketFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/apps.connections.open":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer synthetic-app-token" {
				t.Error("unexpected app token request")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// Each opening request produces a different WebSocket ticket.
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true,
				"url": "wss://wss-fixture.slack.com/link?ticket=" + strconv.Itoa(int(opened.Add(1)))})
		case "/link":
			number := connections.Add(1)
			if r.URL.Query().Get("ticket") != strconv.Itoa(int(number)) || r.Header.Get("Authorization") != "" {
				t.Error("expected a fresh ticket without the app token")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
			defer cancel()
			send := func(data any) bool {
				if err := wsjson.Write(ctx, conn, data); err != nil {
					t.Error(err)
					return false
				}
				return true
			}
			expectACK := func(id string, count int32) bool {
				_, raw, err := conn.Read(ctx)
				if err != nil || strings.TrimSpace(string(raw)) != `{"envelope_id":"`+id+`"}` || persisted.Load() != count {
					t.Errorf("expected ACK %s after %d imports: %s, %v", id, count, raw, err)
					return false
				}
				return true
			}
			send(map[string]any{"type": "hello", "connection_info": map[string]string{"app_id": fixtureBinding.AppID}})
			eventFrame := func(delivery string, callback slackCallback) map[string]any {
				return map[string]any{"type": "events_api", "envelope_id": delivery, "payload": callback}
			}
			first := fixtureCallback("EvFirst")
			second := fixtureCallback("EvSecond")
			switch number {
			case 1:
				rejected := first
				rejected.Event.User = "U99"
				if send(eventFrame("scope-rejected", rejected)) && expectACK("scope-rejected", 0) &&
					send(eventFrame("accepted", first)) && expectACK("accepted", 1) {
					send(map[string]string{"type": "disconnect", "reason": "refresh_requested"})
				}
			case 2:
				if send(eventFrame("replay-different-envelope", first)) && expectACK("replay-different-envelope", 2) &&
					send(eventFrame("transient-failure", second)) {
					_, raw, err := conn.Read(ctx)
					if err == nil || ctx.Err() != nil {
						t.Errorf("expected reconnect without acknowledging the failed import: %s, %v", raw, err)
					}
				}
			case 3:
				if send(eventFrame("retried", second)) && expectACK("retried", 4) {
					send(map[string]string{"type": "disconnect", "reason": "link_disabled"})
				}
			default:
				t.Error("unexpected Socket Mode reconnect")
			}
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	saved := make(map[string]bool)
	err := runSocketMode(ctx, client, fixtureBinding, "synthetic-app-token",
		func(_ context.Context, binding store.SlackBinding, id, goal string) (bool, error) {
			if binding != fixtureBinding || goal != "Review a synthetic snapshot" {
				t.Errorf("unexpected normalized goal for %q", id)
			}
			attempt := persisted.Add(1)
			if attempt == 3 {
				return false, errors.New("transient persistence failure")
			}
			if saved[id] {
				return false, nil
			}
			saved[id] = true
			return true, nil
		}, time.Millisecond)
	if !errors.Is(err, errSocketDisabled) || opened.Load() != 3 || connections.Load() != 3 {
		t.Fatalf("expected two reconnects followed by disable: %v", err)
	}
	if len(saved) != 2 || !saved["EvFirst"] || !saved["EvSecond"] {
		t.Fatalf("unexpected durable identities: %v", saved)
	}
}

func TestOpenSocketRejectsUntrustedURLsAndAuth(t *testing.T) {
	for _, scenario := range []struct {
		response string
		want     error
	}{
		{`{"ok":false,"error":"invalid_auth"}`, errSocketAuth},
		{`{"ok":true,"url":"ws://wss-fixture.slack.com/link"}`, errSocketURL},
		{`{"ok":true,"url":"wss://wss-fixture.slack.com.evil.invalid/link"}`, errSocketURL},
		{`{"ok":true,"url":"wss://user@wss-fixture.slack.com/link"}`, errSocketURL},
	} {
		t.Run(scenario.response, func(t *testing.T) {
			client := socketFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, scenario.response)
			})
			_, _, err := openSocket(context.Background(), client, "synthetic-app-token")
			if !errors.Is(err, scenario.want) || strings.Contains(err.Error(), "synthetic-app-token") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSocketModeRejectsGreetingFromOtherApp(t *testing.T) {
	client := socketFixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := wsjson.Write(ctx, conn, map[string]any{
			"type": "hello", "connection_info": map[string]string{"app_id": "A99"},
		}); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := socketSession(ctx, client, "wss://wss-fixture.slack.com/link", fixtureBinding,
		func(context.Context, store.SlackBinding, string, string) (bool, error) {
			t.Fatal("wrong app must not reach task intake")
			return false, nil
		})
	if !errors.Is(err, errSocketApp) {
		t.Fatalf("expected rejection of the wrong Slack app, got %v", err)
	}
}
