package slackrelay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nep-ai/difqo/internal/slackintake"
	"github.com/nep-ai/difqo/internal/store"
)

func TestSlackWebhookChallengeRequiresFreshSignature(t *testing.T) {
	f := newTestFixture(t)
	challenge := []byte("{\n  \"type\": \"url_verification\", \"challenge\": \"synthetic-challenge\"\n}")
	now := f.time()
	for _, tc := range []struct {
		name   string
		stamp  time.Time
		secret string
		body   []byte
		want   int
	}{
		{"unsigned", now, "", challenge, http.StatusUnauthorized},
		{"bad signature", now, "wrong-secret", challenge, http.StatusUnauthorized},
		{"stale", now.Add(-signatureAge - time.Second), testSigningSecret, challenge, http.StatusUnauthorized},
		{"future", now.Add(signatureAge + time.Second), testSigningSecret, challenge, http.StatusUnauthorized},
		{"malformed JSON", now, testSigningSecret, []byte("{"), http.StatusBadRequest},
		{"missing challenge", now, testSigningSecret, []byte(`{"type":"url_verification"}`), http.StatusBadRequest},
		{"oversized", now, testSigningSecret, bytes.Repeat([]byte(" "), maxWebhookBytes+1), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := f.webhookRequest(tc.body, tc.stamp, tc.secret)
			sendWebhook(t, f.server.Client(), req, tc.want)
		})
	}

	req := f.webhookRequest(challenge, now, testSigningSecret)
	tampered := bytes.ReplaceAll(challenge, []byte("synthetic"), []byte("modified"))
	req.Body = io.NopCloser(bytes.NewReader(tampered))
	req.ContentLength = int64(len(tampered))
	sendWebhook(t, f.server.Client(), req, http.StatusUnauthorized)
	if got := sendWebhook(t, f.server.Client(), f.webhookRequest(challenge, now, testSigningSecret), http.StatusOK); got != "synthetic-challenge" {
		t.Fatalf("challenge response = %q", got)
	}
	if n := f.queueCount(""); n != 0 {
		t.Fatalf("challenge created %d queued events", n)
	}
}

func TestSlackWebhookRequiresClaimedExactDM(t *testing.T) {
	f := newTestFixture(t)
	event := f.fixtureEvent()
	f.webhook(event, http.StatusOK)
	if n := f.queueCount(""); n != 0 {
		t.Fatal("webhook queued a goal before pairing")
	}
	binding := f.prepareBinding()
	f.webhook(event, http.StatusOK)
	if n := f.queueCount(""); n != 0 {
		t.Fatal("webhook queued a goal before local claim")
	}

	f.claim(binding, strings.Repeat("f", 64), http.StatusUnauthorized)
	wrong := binding
	wrong.OperatorID = testAdmin
	f.claim(wrong, f.proof, http.StatusConflict)
	f.claim(binding, f.proof, http.StatusNoContent)
	f.claim(binding, f.proof, http.StatusNoContent)
	for _, tc := range []struct {
		name   string
		change func(*slackintake.Callback)
	}{
		{"other app", func(e *slackintake.Callback) { e.AppID = "A999" }},
		{"other workspace", func(e *slackintake.Callback) { e.WorkspaceID = "T999" }},
		{"installer", func(e *slackintake.Callback) { e.Event.User = testAdmin }},
		{"other DM", func(e *slackintake.Callback) { e.Event.Channel = "D999" }},
		{"group DM", func(e *slackintake.Callback) { e.Event.ChannelType = "mpim" }},
		{"shared conversation", func(e *slackintake.Callback) { e.ExternalShare = true }},
		{"foreign sender", func(e *slackintake.Callback) { e.Event.UserTeam = "T999" }},
		{"foreign message", func(e *slackintake.Callback) { e.Event.Team = "T999" }},
		{"bot ID", func(e *slackintake.Callback) { e.Event.BotID = "B999" }},
		{"bot profile", func(e *slackintake.Callback) { e.Event.BotProfile = &struct{}{} }},
		{"edited message", func(e *slackintake.Callback) { e.Event.Subtype = "message_changed" }},
		{"malformed event ID", func(e *slackintake.Callback) { e.EventID = "??" }},
		{"ordinary chat", func(e *slackintake.Callback) { e.Event.Text = "hello" }},
		{"oversized goal", func(e *slackintake.Callback) { e.Event.Text = "goal " + strings.Repeat("x", 4097) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := f.fixtureEvent()
			tc.change(&invalid)
			raw := mustJSON(t, invalid)
			sendWebhook(t, f.server.Client(), f.webhookRequest(raw, f.time(), testSigningSecret), http.StatusOK)
			if n := f.queueCount(""); n != 0 {
				t.Errorf("unmatched event created %d queue entries", n)
			}
		})
	}
	f.webhook(event, http.StatusOK)
	var goal string
	if err := f.db.QueryRow(context.Background(), `
		SELECT goal FROM slack_relay_events WHERE app_id = $1 AND workspace_id = $2 AND pairing_id = $3 AND event_id = $4
	`, f.appID, testWorkspace, f.id, event.EventID).Scan(&goal); err != nil || goal != "review synthetic work" {
		t.Fatalf("queued goal = %q, query error = %v", goal, err)
	}
}

func TestSlackWebhookDeduplicatesAcrossRetryRestartAndReconnect(t *testing.T) {
	f := newTestFixture(t)
	binding := f.prepareBinding()
	f.claim(binding, f.proof, http.StatusNoContent)
	first := f.id
	event := f.fixtureEvent()
	raw := mustJSON(t, event)
	results := make(chan int, 2)
	for range 2 {
		go func() {
			res, err := f.server.Client().Do(f.webhookRequest(raw, f.time(), testSigningSecret))
			if err != nil {
				results <- 0
				return
			}
			res.Body.Close()
			results <- res.StatusCode
		}()
	}
	for range 2 {
		if code := <-results; code != http.StatusOK {
			t.Fatalf("concurrent webhook returned HTTP %d", code)
		}
	}
	f.restart()
	f.webhook(event, http.StatusOK)
	if n := f.queueCount(""); n != 1 {
		t.Fatalf("retries created %d queue entries", n)
	}

	// The TTL belongs to the unclaimed OAuth pairing. The confirmed route
	// remains active until the proof holder revokes it.
	f.expire()
	f.poll("ready_to_link")
	next := f.fixtureEvent()
	next.EventID = event.EventID + "NEXT"
	f.webhook(next, http.StatusOK)
	if n := f.queueCount(first); n != 2 {
		t.Fatalf("confirmed pairing did not survive OAuth expiry: got %d queued goals", n)
	}

	f.id = randomSecret()[:32]
	second := f.id
	newBinding := f.prepareBinding()
	f.claim(newBinding, f.proof, http.StatusConflict)
	f.request("DELETE", "/pairings/"+first, f.proof, nil, http.StatusNoContent, nil)
	f.webhook(next, http.StatusOK)
	if n := f.queueCount(first); n != 0 {
		t.Fatalf("revoked pairing retained %d queued goals", n)
	}
	f.claim(newBinding, f.proof, http.StatusNoContent)
	f.webhook(event, http.StatusOK)
	f.webhook(next, http.StatusOK)
	if n := f.queueCount(""); n != 2 || f.queueCount(second) != 0 {
		t.Fatal("reconnect replay requeued an old event")
	}
	next.EventID += "NEW"
	f.webhook(next, http.StatusOK)
	if n := f.queueCount(second); n != 1 {
		t.Fatalf("new binding got %d new goals", n)
	}
}

func TestSlackWebhookAsksForRetryOnQueueFailure(t *testing.T) {
	f := newTestFixture(t)
	f.claim(f.prepareBinding(), f.proof, http.StatusNoContent)
	event := f.fixtureEvent()
	f.db.Close()
	challenge := []byte(`{"type":"url_verification","challenge":"fixture-offline"}`)
	if got := sendWebhook(t, f.server.Client(), f.webhookRequest(challenge, f.time(), testSigningSecret), http.StatusOK); got != "fixture-offline" {
		t.Fatalf("challenge during DB outage = %q", got)
	}
	f.webhook(event, http.StatusServiceUnavailable)
	f.restart()
	f.webhook(event, http.StatusOK)
	if n := f.queueCount(f.id); n != 1 {
		t.Fatalf("retry after PostgreSQL failure queued %d goals", n)
	}
}

func (f *fixture) fixtureEvent() slackintake.Callback {
	cb := slackintake.Callback{Type: "event_callback", EventID: "Ev" + randomSecret()[:30],
		AppID: f.appID, WorkspaceID: testWorkspace}
	cb.Event.Type, cb.Event.ChannelType = "message", "im"
	cb.Event.User, cb.Event.Channel = testOperator, testChannel
	cb.Event.Text = " goal  review synthetic work  "
	return cb
}

func (f *fixture) prepareBinding() store.SlackBinding {
	f.t.Helper()
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	f.install(f.start(installApp), "admin", http.StatusNoContent)
	v := f.poll("ready_to_link")
	if v.Binding == nil {
		f.t.Fatal("no binding to claim")
	}
	return *v.Binding
}

func (f *fixture) claim(binding store.SlackBinding, proof string, want int) {
	f.t.Helper()
	req, err := http.NewRequest("POST", f.server.URL+"/pairings/"+f.id+"/claim",
		bytes.NewReader(mustJSON(f.t, binding)))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+proof)
	sendWebhook(f.t, f.server.Client(), req, want)
}

func (f *fixture) webhook(event slackintake.Callback, want int) {
	f.t.Helper()
	req := f.webhookRequest(mustJSON(f.t, event), f.time(), testSigningSecret)
	sendWebhook(f.t, f.server.Client(), req, want)
}

func (f *fixture) webhookRequest(body []byte, now time.Time, secret string) *http.Request {
	req, err := http.NewRequest("POST", f.server.URL+"/slack/events", bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	stamp := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set("X-Slack-Request-Timestamp", stamp)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(append([]byte("v0:"+stamp+":"), body...))
		req.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	}
	return req
}

func sendWebhook(t *testing.T, client *http.Client, req *http.Request, want int) string {
	t.Helper()
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != want {
		t.Fatalf("%s: HTTP %d, want %d (read error %v)", req.URL.Path, res.StatusCode, want, err)
	}
	return string(body)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *fixture) queueCount(pairing string) int {
	f.t.Helper()
	var n int
	err := f.db.QueryRow(context.Background(), `
		SELECT count(*) FROM slack_relay_events
		WHERE app_id = $1 AND ($2 = '' OR (pairing_id = $2 AND goal IS NOT NULL))
	`, f.appID, pairing).Scan(&n)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}
