package slackrelay

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	testWorkspace     = "T123"
	testOperator      = "U123"
	testAdmin         = "U456"
	testClient        = "fixture-client"
	testSecret        = "fixture-secret"
	testSigningSecret = "fixture-signing-secret"
	testBot           = "fixture-bot-token"
	testChannel       = "D123"
)

type fixture struct {
	t                                            *testing.T
	server, slack                                *httptest.Server
	now                                          atomic.Int64
	identityExchanges, installExchanges, dmCalls atomic.Int32
	id, proof, appID                             string
	db                                           *pgxpool.Pool
	dsn                                          string
	tokenKey                                     [32]byte
	wrapRelay                                    func(http.Handler) http.Handler
	blockInstall                                 chan struct{}
	exchanging                                   chan struct{}
	blockDM                                      chan struct{}
	resolving                                    chan struct{}
}

func newTestFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("DIFQO_SLACK_RELAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set DIFQO_SLACK_RELAY_TEST_DATABASE_URL to a migrated disposable relay database")
	}
	f := &fixture{t: t, dsn: dsn, id: randomSecret()[:32], proof: randomSecret(),
		appID: "A" + strings.ToUpper(randomSecret()[:18])}
	rand.Read(f.tokenKey[:])
	f.now.Store(time.Now().Unix())
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.slack = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/openid/connect/keys":
			encode := base64.RawURLEncoding.EncodeToString
			writeJSON(w, map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fixture",
				"alg": "RS256", "n": encode(key.N.Bytes()), "e": encode(big.NewInt(int64(key.E)).Bytes())}}})
		case "/api/openid.connect.token", "/api/oauth.v2.access":
			client, secret, ok := req.BasicAuth()
			if req.URL.Path == "/api/openid.connect.token" {
				client, secret, ok = req.PostFormValue("client_id"), req.PostFormValue("client_secret"), !ok
			}
			if !ok || client != testClient || secret != testSecret || req.Method != "POST" {
				t.Error("invalid exchange authentication")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.exchange(w, req, key)
		case "/api/conversations.open":
			f.dmCalls.Add(1)
			token := req.Header.Get("Authorization")
			if req.Method != "POST" || req.PostFormValue("users") != testOperator || req.PostFormValue("return_im") != "true" {
				t.Error("DM must use exactly the verified operator and request the DM type")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			switch token {
			case "Bearer fixture-block-dm":
				close(f.resolving)
				<-f.blockDM
				fallthrough
			case "Bearer " + testBot:
				writeJSON(w, map[string]any{"ok": true, "channel": map[string]any{"id": testChannel, "is_im": true, "user": testOperator}})
			case "Bearer fixture-revoked":
				writeJSON(w, map[string]any{"ok": false, "error": "token_revoked"})
			case "Bearer fixture-wrong-dm":
				writeJSON(w, map[string]any{"ok": true, "channel": map[string]any{"id": testChannel, "is_im": true, "user": testAdmin}})
			default:
				t.Error("missing installed bot authorization")
				w.WriteHeader(http.StatusUnauthorized)
			}
		default:
			t.Errorf("unexpected Slack endpoint %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.slack.Close)
	f.startRelay("")
	t.Cleanup(func() {
		f.server.Close()
		f.db.Close()
	})
	return f
}

func (f *fixture) startRelay(address string) {
	f.t.Helper()
	config, err := pgxpool.ParseConfig(f.dsn)
	if err != nil {
		f.t.Fatal("parse disposable relay database URL")
	}
	config.ConnConfig.RuntimeParams["application_name"] = f.appID
	f.db, err = pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		f.t.Fatal("open disposable relay database")
	}
	f.server = httptest.NewUnstartedServer(nil)
	if address != "" {
		f.server.Listener.Close()
		f.server.Listener, err = net.Listen("tcp4", address)
		if err != nil {
			f.t.Fatalf("restart relay listener: %v", err)
		}
	}
	handler, err := newFixture(f.db, f.tokenKey, "http://"+f.server.Listener.Addr().String(), f.slack.URL, testClient, testSecret, f.appID, testSigningSecret, f.time)
	if err != nil {
		f.t.Fatal(err)
	}
	if f.wrapRelay != nil {
		handler = f.wrapRelay(handler)
	}
	f.server.Config.Handler = handler
	f.server.Start()
}

func (f *fixture) restart() {
	f.t.Helper()
	address := f.server.Listener.Addr().String()
	f.server.Close()
	f.db.Close()
	f.startRelay(address)
}

func (f *fixture) expire() {
	f.t.Helper()
	_, err := f.db.Exec(context.Background(), `
		UPDATE slack_relay_pairings SET expires_at = clock_timestamp() - interval '1 second'
		WHERE app_id = $1 AND id = $2
	`, f.appID, f.id)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) time() time.Time { return time.Unix(f.now.Load(), 0) }

func (f *fixture) exchange(w http.ResponseWriter, req *http.Request, key *rsa.PrivateKey) {
	code := req.PostFormValue("code")
	if req.URL.Path == "/api/openid.connect.token" {
		f.identityExchanges.Add(1)
		if req.PostFormValue("redirect_uri") != f.server.URL+"/oauth/identity" || req.PostFormValue("grant_type") != "authorization_code" {
			f.t.Error("wrong identity callback or grant type")
		}
		variant, nonce, _ := strings.Cut(code, ":")
		claims := map[string]any{"iss": f.slack.URL, "aud": testClient, "sub": testOperator,
			"exp": f.time().Add(time.Minute).Unix(), "iat": f.time().Unix(),
			"nonce": nonce, "https://slack.com/team_id": testWorkspace, "https://slack.com/user_id": testOperator}
		switch variant {
		case "issuer":
			claims["iss"] = "https://incorrect.example"
		case "audience":
			claims["aud"] = "other-client"
		case "multiple-audiences":
			claims["aud"] = []string{testClient, "other-client"}
		case "expired":
			claims["exp"] = f.time().Add(-time.Hour).Unix()
		case "nonce":
			claims["nonce"] = "other-nonce"
		case "subject":
			claims["sub"] = testAdmin
		case "other-operator":
			claims["sub"], claims["https://slack.com/user_id"] = testAdmin, testAdmin
		case "workspace":
			claims["https://slack.com/team_id"] = "T999"
		}
		writeJSON(w, map[string]any{"ok": true, "id_token": signIdentity(f.t, key, claims, variant == "signature")})
		return
	}

	f.installExchanges.Add(1)
	if req.PostFormValue("redirect_uri") != f.server.URL+"/oauth/install" {
		f.t.Error("wrong install callback")
	}
	app, team, token, scopes, enterprise := f.appID, testWorkspace, testBot, botScopes, false
	switch code {
	case "wrong-workspace":
		team = "T999"
	case "wrong-app":
		app = "A999"
	case "org":
		enterprise = true
	case "missing-scope":
		scopes = "im:history,im:write"
	case "revoked":
		token = "fixture-revoked"
	case "unavailable":
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	case "blocked-install":
		close(f.exchanging)
		<-f.blockInstall
	case "blocked-dm":
		token = "fixture-block-dm"
	case "wrong-dm":
		token = "fixture-wrong-dm"
	}
	writeJSON(w, map[string]any{"ok": true, "app_id": app, "team": map[string]string{"id": team},
		"is_enterprise_install": enterprise, "token_type": "bot", "access_token": token, "scope": scopes,
		"authed_user": map[string]string{"id": testAdmin}})
}

func signIdentity(t *testing.T, key *rsa.PrivateKey, claims map[string]any, badSignature bool) string {
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Error(err)
		return ""
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode([]byte(`{"alg":"RS256","kid":"fixture"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Error(err)
		return ""
	}
	if badSignature {
		sig[0] ^= 1
	}
	return unsigned + "." + encode(sig)
}

func (f *fixture) request(method, path, proof string, form url.Values, want int, out any) {
	f.t.Helper()
	req, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		f.t.Fatal(err)
	}
	if proof != "" {
		req.Header.Set("Authorization", "Bearer "+proof)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := f.server.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		f.t.Fatalf("%s %s: got HTTP %d, want %d", method, strings.Split(path, "?")[0], res.StatusCode, want)
	}
	if out != nil && json.NewDecoder(res.Body).Decode(out) != nil {
		f.t.Fatal("decode relay response")
	}
}

func (f *fixture) poll(want string) pairingStatus {
	f.t.Helper()
	var status pairingStatus
	f.request("PUT", "/pairings/"+f.id, f.proof, nil, http.StatusOK, &status)
	if status.State != want {
		f.t.Fatalf("got relay status %+v, want %s", status, want)
	}
	return status
}

func (f *fixture) start(kind flowKind) url.Values {
	f.t.Helper()
	var reply struct{ URL string }
	f.request("POST", "/pairings/"+f.id+"/"+string(kind), f.proof, nil, http.StatusOK, &reply)
	u, err := url.Parse(reply.URL)
	if err != nil || u.Scheme+"://"+u.Host != f.slack.URL {
		f.t.Fatal("wrong authorize origin")
	}
	v := u.Query()
	if v.Get("redirect_uri") != f.server.URL+"/oauth/"+string(kind) ||
		v.Get("client_id") != testClient || !proofID.MatchString(v.Get("state")) ||
		strings.Contains(reply.URL, f.proof) || strings.Contains(reply.URL, testSecret) {
		f.t.Fatal("invalid browser authorize URL")
	}
	if kind == signIn {
		if u.Path != "/openid/connect/authorize" || v.Get("scope") != "openid profile" ||
			v.Get("response_type") != "code" || v.Get("response_mode") != "form_post" || !proofID.MatchString(v.Get("nonce")) {
			f.t.Fatal("incorrect operator identity flow")
		}
	} else if u.Path != "/oauth/v2/authorize" || v.Get("scope") != botScopes || v.Has("nonce") || v.Has("user_scope") {
		f.t.Fatal("incorrect workspace installation flow")
	}
	return v
}

func (f *fixture) identity(v url.Values, code string, want int) {
	f.t.Helper()
	f.request("POST", "/oauth/identity", "", url.Values{"state": {v.Get("state")}, "code": {code + ":" + v.Get("nonce")}}, want, nil)
}

func (f *fixture) install(v url.Values, code string, want int) {
	f.t.Helper()
	f.request("GET", "/oauth/install?"+url.Values{"state": {v.Get("state")}, "code": {code}}.Encode(), "", nil, want, nil)
}

func TestOfficialPairingSeparatesAdminAndOperator(t *testing.T) {
	for _, installFirst := range []bool{false, true} {
		name := "identity first"
		if installFirst {
			name = "install first"
		}
		t.Run(name, func(t *testing.T) {
			f := newTestFixture(t)
			f.poll("connecting")
			if installFirst {
				f.install(f.start(installApp), "admin", http.StatusNoContent)
				v := f.poll("installed")
				if v.Installed != testWorkspace || v.Operator != "" || v.Binding != nil || f.dmCalls.Load() != 0 {
					t.Fatalf("installation granted operator authority: %+v", v)
				}
			}
			f.identity(f.start(signIn), "operator", http.StatusNoContent)
			if !installFirst {
				v := f.poll("connecting")
				if v.Workspace != testWorkspace || v.Operator != testOperator || v.Installed != "" || v.Binding != nil {
					t.Fatalf("unexpected verified identity: %+v", v)
				}
				install := f.start(installApp)
				if install.Get("team") != testWorkspace {
					t.Fatal("verified workspace must be the installation hint")
				}
				f.install(install, "admin", http.StatusNoContent)
			}
			f.request("PUT", "/pairings/"+f.id, "", nil, http.StatusUnauthorized, nil)
			f.request("PUT", "/pairings/"+f.id, strings.Repeat("c", 64), nil, http.StatusUnauthorized, nil)
			if f.dmCalls.Load() != 0 {
				t.Fatal("unauthenticated poll opened a DM")
			}
			v := f.poll("ready_to_link")
			if v.Binding == nil || v.Binding.AppID != f.appID || v.Binding.WorkspaceID != testWorkspace ||
				v.Binding.OperatorID != testOperator || v.Binding.ChannelID != testChannel {
				t.Fatalf("unexpected binding: %+v", v.Binding)
			}
			f.poll("ready_to_link")
			if f.dmCalls.Load() != 1 {
				t.Fatal("serial poll resolved an existing DM again")
			}
		})
	}
}

func TestOfficialPairingPendingApprovalAndResumption(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	install := f.start(installApp)
	f.request("GET", "/oauth/install?"+url.Values{"state": {install.Get("state")}, "error": {"access_denied"}}.Encode(), "", nil, http.StatusNoContent, nil)
	f.poll("connecting")
	awaitingApproval := f.start(installApp)
	f.request("POST", "/pairings/"+f.id+"/approval-needed", "", nil, http.StatusUnauthorized, nil)
	f.request("POST", "/pairings/"+f.id+"/approval-needed", f.proof, nil, http.StatusNoContent, nil)
	status := f.poll("approval_needed")
	if status.Operator != testOperator || status.Installed != "" || status.Binding != nil || f.installExchanges.Load() != 0 {
		t.Fatalf("unexpected pending status: %+v", status)
	}
	resumed := f.start(installApp)
	if resumed.Get("state") == awaitingApproval.Get("state") {
		t.Fatal("installation retry must use fresh state")
	}
	f.poll("approval_needed")
	f.install(awaitingApproval, "admin", http.StatusConflict)
	f.install(resumed, "admin", http.StatusNoContent)
	f.poll("ready_to_link")
}

func TestOfficialPairingValidatesProviderResults(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	for _, variant := range []string{"issuer", "audience", "multiple-audiences", "expired", "nonce", "subject", "signature"} {
		t.Run(variant, func(t *testing.T) {
			f.identity(f.start(signIn), variant, http.StatusBadGateway)
			if f.poll("connecting").Operator != "" {
				t.Fatal("invalid token verified a Slack user")
			}
		})
	}
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	for _, variant := range []string{"wrong-app", "org", "missing-scope", "wrong-workspace"} {
		t.Run(variant, func(t *testing.T) {
			want := http.StatusBadGateway
			if variant == "wrong-workspace" {
				want = http.StatusConflict
			}
			f.install(f.start(installApp), variant, want)
			if f.poll("connecting").Installed != "" || f.dmCalls.Load() != 0 {
				t.Fatal("invalid installation opened a DM")
			}
		})
	}
	f.install(f.start(installApp), "wrong-dm", http.StatusNoContent)
	if f.poll("installed").Binding != nil {
		t.Fatal("admin's DM was accepted")
	}
}

func TestOfficialPairingPreinstalledWorkspaceCannotOverrideIdentity(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	f.install(f.start(installApp), "wrong-workspace", http.StatusNoContent)
	f.poll("installed")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	v := f.poll("connecting")
	if v.Workspace != testWorkspace || v.Installed != "" || f.dmCalls.Load() != 0 {
		t.Fatalf("unexpected status after mismatched installation: %+v", v)
	}
}

func TestOfficialPairingRejectsExpiredAndReplayedCallbacks(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	stale := f.start(signIn)
	identity := f.start(signIn)
	f.identity(stale, "operator", http.StatusConflict)
	f.install(identity, "admin", http.StatusConflict)
	f.identity(identity, "operator", http.StatusNoContent)
	f.identity(identity, "operator", http.StatusConflict)
	install := f.start(installApp)
	f.identity(install, "operator", http.StatusConflict)
	f.expire()
	f.restart()
	f.install(install, "admin", http.StatusConflict)
	f.poll("reconnect_required")
	if f.identityExchanges.Load() != 1 || f.installExchanges.Load() != 0 {
		t.Fatal("invalid callbacks reached Slack token endpoints")
	}
	f.id = randomSecret()[:32]
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	install = f.start(installApp)
	f.request("HEAD", "/oauth/install?"+url.Values{"state": {install.Get("state")}, "code": {"admin"}}.Encode(), "", nil, http.StatusMethodNotAllowed, nil)
	f.install(install, "admin", http.StatusNoContent)
	f.install(install, "admin", http.StatusConflict)
	f.poll("ready_to_link")
}

func TestOfficialPairingReconnectAfterRevokedInstallation(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	f.install(f.start(installApp), "revoked", http.StatusNoContent)
	if f.poll("reconnect_required").Binding != nil {
		t.Fatal("revoked installation exposed a binding")
	}
	f.restart()
	f.poll("reconnect_required")
	f.request("POST", "/pairings/"+f.id+"/install", f.proof, nil, http.StatusConflict, nil)
	f.id = randomSecret()[:32]
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	f.install(f.start(installApp), "admin", http.StatusNoContent)
	f.poll("ready_to_link")
}

func TestOfficialPairingFixtureRejectsRemoteOrigins(t *testing.T) {
	for _, origin := range []string{"https://slack.com", "http://localhost:8080", "http://127.0.0.1:8080/api"} {
		if _, err := NewFixture(new(pgxpool.Pool), [32]byte{1}, "http://127.0.0.1:3210", origin, testClient, testSecret, "A123", testSigningSecret); err == nil {
			t.Errorf("accepted non-fixture Slack origin %q", origin)
		}
	}
}

func TestOfficialPairingResumesAcrossRelayRestarts(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	install := f.start(installApp)
	f.restart()
	f.install(install, "admin", http.StatusNoContent)
	f.restart()
	f.install(install, "admin", http.StatusConflict)
	if v := f.poll("installed"); v.Operator != "" || v.Installed != testWorkspace || v.Binding != nil {
		t.Fatalf("admin installation changed operator identity: %+v", v)
	}

	// The test provider token survives only as authenticated ciphertext until
	// the authorized poll resolves the DM.
	var sealed []byte
	if err := f.db.QueryRow(context.Background(), `
		SELECT sealed_bot_token FROM slack_relay_pairings WHERE app_id = $1 AND id = $2
	`, f.appID, f.id).Scan(&sealed); err != nil || len(sealed) == 0 || strings.Contains(string(sealed), testBot) {
		t.Fatal("expected a sealed fixture bot token")
	}
	identity := f.start(signIn)
	f.restart()
	f.identity(identity, "operator", http.StatusNoContent)
	f.restart()
	f.identity(identity, "operator", http.StatusConflict)
	ready := f.poll("ready_to_link")
	f.restart()
	reloaded := f.poll("ready_to_link")
	if ready.Binding == nil || reloaded.Binding == nil || *ready.Binding != *reloaded.Binding ||
		reloaded.Operator != testOperator || f.dmCalls.Load() != 1 {
		t.Fatalf("binding was lost or replaced across restart: %+v", reloaded)
	}
	if err := f.db.QueryRow(context.Background(), `
		SELECT sealed_bot_token FROM slack_relay_pairings WHERE app_id = $1 AND id = $2
	`, f.appID, f.id).Scan(&sealed); err != nil || sealed != nil {
		t.Fatal("resolved pairing retained the fixture bot token")
	}
	f.request("DELETE", "/pairings/"+f.id, strings.Repeat("f", 64), nil, http.StatusUnauthorized, nil)
	f.request("DELETE", "/pairings/"+f.id, f.proof, nil, http.StatusNoContent, nil)
	f.restart()
	if v := f.poll("reconnect_required"); v.Operator != "" || v.Installed != "" || v.Binding != nil {
		t.Fatalf("retired pairing exposed the previous binding: %+v", v)
	}
	f.request("PUT", "/pairings/"+f.id, strings.Repeat("f", 64), nil, http.StatusUnauthorized, nil)
}

func TestOfficialPairingPendingSurvivesRelayRestart(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	first := f.start(installApp)
	f.request("POST", "/pairings/"+f.id+"/approval-needed", f.proof, nil, http.StatusNoContent, nil)
	f.restart()
	v := f.poll("approval_needed")
	if v.Workspace != testWorkspace || v.Operator != testOperator || v.Installed != "" || v.Binding != nil {
		t.Fatalf("unexpected saved approval state: %+v", v)
	}
	next := f.start(installApp)
	f.restart()
	f.install(first, "admin", http.StatusConflict)
	f.install(next, "admin", http.StatusNoContent)
	f.poll("ready_to_link")
}

func TestOfficialPairingRevocationFencesInFlightExchanges(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	install := f.start(installApp)
	f.blockInstall, f.exchanging = make(chan struct{}), make(chan struct{})
	result := f.asyncRequest("GET", "/oauth/install?"+url.Values{
		"state": {install.Get("state")}, "code": {"blocked-install"},
	}.Encode(), "", nil)
	awaitProvider(t, f.exchanging, f.blockInstall)
	f.install(install, "admin", http.StatusConflict)
	f.request("DELETE", "/pairings/"+f.id, f.proof, nil, http.StatusNoContent, nil)
	close(f.blockInstall)
	if code := <-result; code != http.StatusConflict || f.installExchanges.Load() != 1 {
		t.Fatalf("callback was replayed or committed after revocation (HTTP %d)", code)
	}
	f.restart()
	f.install(install, "admin", http.StatusConflict)
	f.poll("reconnect_required")

	f.id = randomSecret()[:32]
	f.poll("connecting")
	f.install(f.start(installApp), "blocked-dm", http.StatusNoContent)
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	f.blockDM, f.resolving = make(chan struct{}), make(chan struct{})
	var inFlight pairingStatus
	result = f.asyncRequest("PUT", "/pairings/"+f.id, f.proof, &inFlight)
	awaitProvider(t, f.resolving, f.blockDM)
	f.request("DELETE", "/pairings/"+f.id, f.proof, nil, http.StatusNoContent, nil)
	close(f.blockDM)
	if code := <-result; code != http.StatusOK || inFlight.State != "reconnect_required" || inFlight.Binding != nil {
		t.Fatalf("fenced poll returned HTTP %d and status %+v", code, inFlight)
	}
	f.restart()
	f.poll("reconnect_required")
}

func (f *fixture) asyncRequest(method, path, proof string, status *pairingStatus) <-chan int {
	f.t.Helper()
	req, err := http.NewRequest(method, f.server.URL+path, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if proof != "" {
		req.Header.Set("Authorization", "Bearer "+proof)
	}
	return f.asyncHTTP(req, status)
}

func (f *fixture) asyncHTTP(req *http.Request, status *pairingStatus) <-chan int {
	client := f.server.Client()
	done := make(chan int, 1)
	go func() {
		res, err := client.Do(req)
		if err != nil {
			done <- 0
			return
		}
		defer res.Body.Close()
		if status != nil && res.StatusCode == http.StatusOK {
			if err := json.NewDecoder(res.Body).Decode(status); err != nil {
				f.t.Errorf("decode relay response: %v", err)
			}
		}
		done <- res.StatusCode
	}()
	return done
}

func awaitProvider(t *testing.T, entered <-chan struct{}, release chan struct{}) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture provider call did not start")
	}
}

func TestOfficialPairingRetriesWithFreshStateAfterExchangeFails(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	install := f.start(installApp)
	f.install(install, "unavailable", http.StatusBadGateway)
	f.restart()
	f.install(install, "admin", http.StatusConflict)
	if v := f.poll("connecting"); v.Operator != testOperator || v.Installed != "" || f.installExchanges.Load() != 1 {
		t.Fatalf("failed exchange left incorrect pairing state: %+v", v)
	}
	f.install(f.start(installApp), "admin", http.StatusNoContent)
	f.poll("ready_to_link")
}

func TestOfficialPairingExpiredCallbackClearsInstalledState(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	f.install(f.start(installApp), "admin", http.StatusNoContent)
	identity := f.start(signIn)
	f.expire()
	f.restart()
	f.identity(identity, "operator", http.StatusConflict)
	if f.identityExchanges.Load() != 0 {
		t.Fatal("expired callback reached Slack token endpoint")
	}
	f.assertRetired()
	f.poll("reconnect_required")
}

func TestOfficialPairingExpiresDuringTokenExchange(t *testing.T) {
	f := newTestFixture(t)
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	install := f.start(installApp)
	f.blockInstall, f.exchanging = make(chan struct{}), make(chan struct{})
	result := f.asyncRequest("GET", "/oauth/install?"+url.Values{
		"state": {install.Get("state")}, "code": {"blocked-install"},
	}.Encode(), "", nil)
	awaitProvider(t, f.exchanging, f.blockInstall)
	f.expire()
	close(f.blockInstall)
	if code := <-result; code != http.StatusConflict {
		t.Fatalf("expired callback returned HTTP %d", code)
	}
	f.assertRetired()
	f.poll("reconnect_required")
}

func (f *fixture) assertRetired() {
	f.t.Helper()
	var scrubbed bool
	err := f.db.QueryRow(context.Background(), `
		SELECT revoked AND verified_workspace = '' AND verified_operator = '' AND installed_workspace = ''
		    AND sealed_bot_token IS NULL AND dm_channel = '' AND NOT approval_pending
		    AND NOT EXISTS (
		        SELECT 1 FROM slack_relay_oauth_flows f WHERE f.app_id = p.app_id AND f.pairing_id = p.id
		    )
		FROM slack_relay_pairings p WHERE app_id = $1 AND id = $2
	`, f.appID, f.id).Scan(&scrubbed)
	if err != nil || !scrubbed {
		f.t.Fatalf("retirement did not clear pairing and OAuth state: %v", err)
	}
}
