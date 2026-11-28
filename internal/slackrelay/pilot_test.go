package slackrelay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nep-ai/difqo/internal/connections"
	"github.com/nep-ai/difqo/internal/slackintake"
)

func TestHostedSlackPilot(t *testing.T) {
	f := newTestFixture(t)
	// An existing fixture pairing, even with the same app and a valid proof,
	// must not become a hosted pairing after the migration.
	f.poll("connecting")
	f.server.Close()
	f.server = httptest.NewUnstartedServer(nil)
	enrollment := randomSecret()
	provider, err := newPilot(f.db, PilotConfig{
		Origin: "https://" + f.server.Listener.Addr().String(),
		AppID:  f.appID, ClientID: testClient, ClientSecret: testSecret, SigningSecret: testSigningSecret,
		WorkspaceID: testWorkspace, OperatorID: testOperator, EnrollmentToken: enrollment, TokenKey: f.tokenKey,
	}, f.slack.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.server.Config.Handler = connections.Handler(provider, func(ctx context.Context) error {
		return CheckSchema(ctx, f.db)
	})
	f.server.StartTLS()

	f.request("GET", "/healthz", "", nil, http.StatusOK, nil)
	f.request("GET", "/readyz", "", nil, http.StatusOK, nil)
	f.request("PUT", "/pairings/"+f.id, f.proof, nil, http.StatusUnauthorized, nil)
	f.id, f.proof = randomSecret()[:32], randomSecret()
	path := "/pairings/" + f.id
	f.request("PUT", path, f.proof, nil, http.StatusUnauthorized, nil)
	f.request("DELETE", path, f.proof, nil, http.StatusUnauthorized, nil)
	f.request("GET", path+"/events", "", nil, http.StatusUnauthorized, nil)

	enroll := func(token, proof string, status int) {
		t.Helper()
		raw, err := json.Marshal(map[string]string{"pairing_id": f.id, "proof": proof})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, f.server.URL+"/slack/enroll", strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		sendWebhook(t, f.server.Client(), req, status)
	}
	enroll(f.proof, f.proof, http.StatusUnauthorized)
	enroll(enrollment, f.proof, http.StatusNoContent)
	enroll(enrollment, f.proof, http.StatusNoContent)
	enroll(enrollment, randomSecret(), http.StatusConflict)
	f.request("PUT", path, enrollment, nil, http.StatusUnauthorized, nil)
	f.poll("connecting")

	badIdentity := f.start(signIn)
	if badIdentity.Get("team") != testWorkspace {
		t.Fatal("identity flow must hint the approved workspace")
	}
	f.identity(badIdentity, "other-operator", http.StatusForbidden)
	f.identity(badIdentity, "approved", http.StatusConflict)
	f.identity(f.start(signIn), "workspace", http.StatusForbidden)
	f.identity(f.start(signIn), "approved", http.StatusNoContent)
	install := f.start(installApp)
	if install.Get("team") != testWorkspace {
		t.Fatal("install flow must hint the approved workspace")
	}
	f.install(install, "wrong-workspace", http.StatusForbidden)
	f.install(f.start(installApp), "approved-admin", http.StatusNoContent)
	ready := f.poll("ready_to_link")
	if ready.Binding == nil || ready.Binding.OperatorID != testOperator {
		t.Fatal("expected only the approved operator to bind")
	}
	event := f.fixtureEvent()
	event.Event.Text = "Review the fixture change."
	f.webhook(event, http.StatusOK)
	if f.queueCount(f.id) != 0 {
		t.Fatal("unclaimed binding queued an event")
	}
	f.claim(*ready.Binding, f.proof, http.StatusNoContent)
	unlinked := event
	unlinked.Event.User = testAdmin
	f.webhook(unlinked, http.StatusOK)
	if f.queueCount(f.id) != 0 {
		t.Fatal("an unlinked sender queued a plain-text goal")
	}
	f.webhook(event, http.StatusOK)
	f.webhook(event, http.StatusOK)
	var batch slackintake.DeliveryBatch
	f.request("GET", path+"/events", f.proof, nil, http.StatusOK, &batch)
	if batch.Binding != *ready.Binding || len(batch.Events) != 1 || batch.Events[0].EventID != event.EventID ||
		batch.Events[0].Goal != event.Event.Text {
		t.Fatalf("unexpected pilot delivery: %+v", batch)
	}
	f.request("PUT", path+"/events/"+event.EventID, f.proof, nil, http.StatusNoContent, nil)
	f.request("DELETE", path, f.proof, nil, http.StatusNoContent, nil)
	f.request("GET", path+"/events", f.proof, nil, http.StatusConflict, nil)
	enroll(enrollment, f.proof, http.StatusConflict)
	f.db.Close()
	f.request("GET", "/healthz", "", nil, http.StatusOK, nil)
	f.request("GET", "/readyz", "", nil, http.StatusServiceUnavailable, nil)
}
