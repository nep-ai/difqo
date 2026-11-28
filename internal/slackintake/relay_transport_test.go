package slackintake

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/nep-ai/difqo/internal/store"
)

func TestRelayOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://Relay.Example:443":      "https://relay.example",
		"https://relay.example:9443":     "https://relay.example:9443",
		"https://[0:0:0:0:0:0:0:1]:8443": "https://[::1]:8443",
		"https://127.0.0.1:8443":         "https://127.0.0.1:8443",
	} {
		c, err := NewHTTPSConnections(store.Tasks{}, raw)
		if err != nil || c.origin != want {
			t.Errorf("origin %q: got %v, want %q", raw, err, want)
		}
	}
	for _, raw := range []string{
		"http://relay.example", "//relay.example", "https:///relay.example",
		"https://user:pass@relay.example", "https://relay.example/", "https://relay.example/path",
		"https://relay.example/%2f", "https://relay.example?", "https://relay.example?q=1",
		"https://relay.example#", "https://relay.example#fragment", "https://relay.example:",
		"https://relay.example:0", "https://relay.example:65536", "https://relay.example:0443",
		"https://relay", "https://relay.example.", "https://relay..example",
		"https://-relay.example", "https://[relay.example]", "https://relKy.example", "https://[fe80::1%25en0]",
	} {
		if _, err := NewHTTPSConnections(store.Tasks{}, raw); err == nil {
			t.Errorf("accepted invalid origin %q", raw)
		}
	}
	if _, err := NewFixtureConnections(store.Tasks{}, "http://127.0.0.1:8765"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://relay.example:8765", "http://localhost:8765", "http://127.0.0.1", "https://127.0.0.1:8765"} {
		if _, err := NewFixtureConnections(store.Tasks{}, raw); err == nil {
			t.Errorf("accepted invalid fixture origin %q", raw)
		}
	}
}

func TestRelayTLSAndOriginFences(t *testing.T) {
	var redirectHits, relayHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayHits.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer relay.Close()

	c, err := NewHTTPSConnections(store.Tasks{}, relay.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := &store.SlackPairing{ID: "1234567890abcdef1234567890abcdef", Proof: "synthetic-proof", Relay: relay.URL}
	revoke := func() error { return c.relayChange(t.Context(), http.MethodDelete, p, "", nil) }
	if err := revoke(); !errors.Is(err, ErrRelayUnavailable) || relayHits.Load() != 0 {
		t.Fatalf("untrusted certificate reached relay handler: %v", err)
	}
	trustTestRelay(c, relay)
	p.Relay = target.URL
	if err := revoke(); !errors.Is(err, store.ErrSlackTransition) || relayHits.Load() != 0 {
		t.Fatalf("stale origin reached relay handler: %v", err)
	}
	p.Relay = relay.URL
	if err := revoke(); !errors.Is(err, ErrRelayUnavailable) || relayHits.Load() != 1 || redirectHits.Load() != 0 {
		t.Fatalf("redirect performed unexpected requests: relay=%d target=%d err=%v", relayHits.Load(), redirectHits.Load(), err)
	}

	_, port, err := net.SplitHostPort(relay.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	p.Relay = "https://wrong-host.example:" + port
	c, err = NewHTTPSConnections(store.Tasks{}, p.Relay)
	if err != nil {
		t.Fatal(err)
	}
	transport := trustTestRelay(c, relay)
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, relay.Listener.Addr().String())
	}
	if err := revoke(); !errors.Is(err, ErrRelayUnavailable) || relayHits.Load() != 1 {
		t.Fatalf("wrong certificate hostname reached relay handler: %v", err)
	}
}

// Replace the transport before use. There is no production CA override.
func trustTestRelay(c *Connections, relay *httptest.Server) *http.Transport {
	transport := c.client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	transport.TLSClientConfig.RootCAs.AddCert(relay.Certificate())
	c.client.Transport = transport
	return transport
}

func TestHTTPSRelayDelivery(t *testing.T) {
	dsn := os.Getenv("DIFQO_SLACK_DELIVERY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set DIFQO_SLACK_DELIVERY_TEST_DATABASE_URL to a migrated disposable local database")
	}
	ctx := t.Context()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid Slack test database configuration")
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := fmt.Sprintf("difqo_https_test_%d", time.Now().UnixNano())
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
	tasks := store.Tasks{DB: db}
	binding := store.SlackBinding{AppID: "AHTTPS", WorkspaceID: "THTTPS", OperatorID: "UHTTPS", ChannelID: "DHTTPS"}
	event := Delivery{EventID: "EvHTTPS", Goal: "inspect the HTTPS fixture"}
	var mu sync.Mutex
	var proof, pairPath string
	var status = relayStatus{State: "connecting"}
	enrollment := strings.Repeat("f", 64)
	firstEnrollment := true
	claimed, receipted, revoked := false, false, false
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost && r.URL.Path == "/slack/enroll" {
			var input struct {
				ID    string `json:"pairing_id"`
				Proof string `json:"proof"`
			}
			if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+enrollment ||
				json.NewDecoder(r.Body).Decode(&input) != nil || !pairingIDPattern.MatchString(input.ID) ||
				!oauthState.MatchString(input.Proof) || input.Proof == enrollment {
				t.Error("invalid local enrollment request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if proof != "" && (proof != "Bearer "+input.Proof || pairPath != "/pairings/"+input.ID) {
				t.Error("enrollment retry changed its pairing or proof")
			}
			proof, pairPath = "Bearer "+input.Proof, "/pairings/"+input.ID
			if firstEnrollment {
				firstEnrollment = false
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		if proof == "" || proof != r.Header.Get("Authorization") || revoked {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPut && r.URL.Path == pairPath:
			json.NewEncoder(w).Encode(status)
		case r.Method == http.MethodPost && r.URL.Path == pairPath+"/identity":
			json.NewEncoder(w).Encode(map[string]string{"url": fixtureOAuthURL("https://"+r.Host, "/identity", binding.WorkspaceID)})
		case r.Method == http.MethodPost && r.URL.Path == pairPath+"/install":
			json.NewEncoder(w).Encode(map[string]string{"url": fixtureOAuthURL("https://"+r.Host, "/install", binding.WorkspaceID)})
		case r.Method == http.MethodPost && r.URL.Path == pairPath+"/approval-needed":
			status.State = "approval_needed"
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == pairPath+"/claim":
			var expected store.SlackBinding
			if json.NewDecoder(r.Body).Decode(&expected) != nil || expected != binding {
				w.WriteHeader(http.StatusConflict)
				return
			}
			claimed = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == pairPath+"/events" && claimed:
			batch := DeliveryBatch{Binding: binding}
			if !receipted {
				batch.Events = []Delivery{event}
			}
			json.NewEncoder(w).Encode(batch)
		case r.Method == http.MethodPut && r.URL.Path == pairPath+"/events/"+event.EventID && claimed:
			var goal string
			err := db.QueryRowContext(r.Context(), `
				SELECT t.goal FROM slack_task_sources s JOIN tasks t ON s.task_id = t.id
				WHERE s.app_id = $1 AND s.workspace_id = $2 AND s.event_id = $3
			`, binding.AppID, binding.WorkspaceID, event.EventID).Scan(&goal)
			if err != nil || goal != event.Goal {
				t.Errorf("receipt without a committed goal: %v", err)
				w.WriteHeader(http.StatusConflict)
				return
			}
			receipted = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == pairPath:
			revoked = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer relay.Close()
	c, err := NewPilotConnections(tasks, relay.URL, enrollment)
	if err != nil {
		t.Fatal(err)
	}
	trustTestRelay(c, relay)
	first, err := c.Begin(ctx)
	if !errors.Is(err, ErrRelayUnavailable) || first.PairingID == "" {
		t.Fatalf("lost enrollment response: %v", err)
	}
	begin, err := c.Begin(ctx)
	if err != nil || begin.PairingID != first.PairingID {
		t.Fatalf("retry enrollment: %v", err)
	}
	if _, err := c.OAuth(ctx, begin.PairingID, "start_install"); !errors.Is(err, store.ErrSlackTransition) {
		t.Fatal("started installation before verifying identity")
	}
	identity, err := c.OAuth(ctx, begin.PairingID, "start_identity")
	if err != nil || identity.OAuthURL != fixtureOAuthURL(relay.URL, "/identity", binding.WorkspaceID) {
		t.Fatalf("identity handoff error: %v", err)
	}
	mu.Lock()
	status.Workspace, status.Operator = binding.WorkspaceID, binding.OperatorID
	mu.Unlock()
	if _, err := c.Refresh(ctx, begin.PairingID); err != nil {
		t.Fatal(err)
	}
	install, err := c.OAuth(ctx, begin.PairingID, "start_install")
	if err != nil || install.OAuthURL != fixtureOAuthURL(relay.URL, "/install", binding.WorkspaceID) {
		t.Fatalf("install handoff error: %v", err)
	}
	if pending, err := c.OAuth(ctx, begin.PairingID, "approval_needed"); err != nil || pending.State != "approval_needed" {
		t.Fatalf("pending approval: state=%q, err=%v", pending.State, err)
	}
	if _, err := c.OAuth(ctx, begin.PairingID, "start_install"); err != nil {
		t.Fatalf("resume install: %v", err)
	}
	mu.Lock()
	status.State, status.Installed, status.Binding = "ready_to_link", binding.WorkspaceID, binding
	mu.Unlock()
	if ready, err := c.Refresh(ctx, begin.PairingID); err != nil || ready.State != "ready_to_link" {
		t.Fatalf("refresh: %s %v", ready.State, err)
	}
	connected, err := c.Confirm(ctx, begin.PairingID, binding)
	if err != nil || connected.State != "connected" {
		t.Fatalf("confirm: %s %v", connected.State, err)
	}
	if err := c.sync(ctx); err != nil {
		t.Fatal(err)
	}
	if disconnected, err := c.Disconnect(ctx, connected.Generation, begin.PairingID); err != nil || disconnected.State != "disconnected" {
		t.Fatalf("disconnect: %s %v", disconnected.State, err)
	}
	mu.Lock()
	if !claimed || !receipted || !revoked {
		mu.Unlock()
		t.Fatal("expected claim, receipt, and revocation over TLS")
	}
	proof, pairPath, revoked, firstEnrollment = "", "", false, true
	mu.Unlock()

	caller, err := NewPilotConnections(tasks, relay.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	trustTestRelay(caller, relay)
	failed, err := caller.BeginWithCode(ctx, enrollment)
	if !errors.Is(err, ErrRelayUnavailable) || failed.PairingID != "" {
		t.Fatalf("failed caller enrollment published a pairing: %+v, %v", failed, err)
	}
	view, err := caller.Status(ctx)
	if err != nil || view.State != "disconnected" || view.PairingID != "" {
		t.Fatalf("failed caller enrollment persisted a pairing: %+v, %v", view, err)
	}
	if _, err := caller.Disconnect(ctx, view.Generation, ""); err != nil {
		t.Fatalf("disconnect after failed enrollment: %v", err)
	}
	mu.Lock()
	orphan := pairPath
	proof, pairPath = "", ""
	mu.Unlock()
	next, err := caller.BeginWithCode(ctx, enrollment)
	if err != nil || next.PairingID == "" || "/pairings/"+next.PairingID == orphan {
		t.Fatalf("caller retry did not create a fresh pairing: %+v, %v", next, err)
	}
	if disconnected, err := caller.Disconnect(ctx, next.Generation, next.PairingID); err != nil ||
		disconnected.State != "disconnected" || disconnected.PairingID != "" {
		t.Fatalf("disconnect without an enrollment code: %+v, %v", disconnected, err)
	}
}

func fixtureOAuthURL(origin, suffix, team string) string {
	q := url.Values{
		"client_id":    {"fixture-client"},
		"state":        {strings.Repeat("a", 64)},
		"redirect_uri": {origin + "/oauth" + suffix},
		"team":         {team},
	}
	path := "/oauth/v2/authorize"
	q.Set("scope", "im:history,im:write,chat:write")
	if suffix == "/identity" {
		path = "/openid/connect/authorize"
		q.Set("scope", "openid profile")
		q.Set("nonce", strings.Repeat("b", 64))
		q.Set("response_type", "code")
		q.Set("response_mode", "form_post")
	}
	return "https://slack.com" + path + "?" + q.Encode()
}
