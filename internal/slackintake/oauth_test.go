package slackintake

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nep-ai/difqo/internal/store"
)

func TestOAuthHandoffRejectsUntrustedURLs(t *testing.T) {
	const origin = "https://relay.example"
	proof := strings.Repeat("c", 64)
	token := strings.Repeat("d", 64)
	c := &Connections{origin: origin, enrollmentToken: token}
	p := &store.SlackPairing{Proof: proof, Workspace: "TAPPROVED"}
	good := fixtureOAuthURL(origin, "/install", p.Workspace)
	if !c.validOAuthURL(good, p, "/install") {
		t.Fatal("expected valid Slack install URL")
	}
	for name, raw := range map[string]string{
		"wrong origin":         strings.Replace(good, "slack.com", "slack.com.example", 1),
		"wrong scheme":         strings.Replace(good, "https://", "http://", 1),
		"credentials":          strings.Replace(good, "slack.com", "local@slack.com", 1),
		"fragment":             good + "#",
		"wrong flow":           fixtureOAuthURL(origin, "/identity", p.Workspace),
		"wrong callback":       fixtureOAuthURL("https://other.example", "/install", p.Workspace),
		"wrong workspace":      fixtureOAuthURL(origin, "/install", "TOTHER"),
		"duplicate state":      good + "&state=" + strings.Repeat("a", 64),
		"unexpected parameter": good + "&extra=x",
	} {
		t.Run(name, func(t *testing.T) {
			if c.validOAuthURL(raw, p, "/install") {
				t.Fatal("untrusted authorization URL was accepted")
			}
		})
	}
	for _, invalid := range []string{"", "not-a-state", token, proof} {
		u, err := url.Parse(good)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("state", invalid)
		u.RawQuery = q.Encode()
		if c.validOAuthURL(u.String(), p, "/install") {
			t.Fatal("accepted invalid or sensitive OAuth state")
		}
	}
}

func TestCancelUnregisteredPilotPairing(t *testing.T) {
	token := strings.Repeat("d", 64)
	proof := strings.Repeat("c", 64)
	id := strings.Repeat("a", 32)
	step := 0
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		credential := r.Header.Get("Authorization")
		switch step {
		case 1, 3:
			if r.Method != http.MethodDelete || r.URL.Path != "/pairings/"+id || credential != "Bearer "+proof {
				t.Error("invalid cancellation request")
			}
			if step == 1 {
				w.WriteHeader(http.StatusUnauthorized)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		case 2:
			if r.Method != http.MethodPost || r.URL.Path != "/slack/enroll" || credential != "Bearer "+token {
				t.Error("invalid enrollment recovery")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected relay request")
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer relay.Close()
	c, err := NewPilotConnections(store.Tasks{}, relay.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	trustTestRelay(c, relay)
	if err := c.revoke(t.Context(), &store.SlackPairing{ID: id, Proof: proof, Relay: relay.URL}); err != nil {
		t.Fatal(err)
	}
}
