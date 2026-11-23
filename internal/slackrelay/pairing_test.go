package slackrelay

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testApp       = "A123"
	testWorkspace = "T123"
	testOperator  = "U123"
	testAdmin     = "U456"
	testClient    = "fixture-client"
	testSecret    = "fixture-secret"
	testBot       = "fixture-bot-token"
	testChannel   = "D123"
)

type fixture struct {
	t                                            *testing.T
	server, slack                                *httptest.Server
	now                                          atomic.Int64
	identityExchanges, installExchanges, dmCalls atomic.Int32
	id, proof                                    string
}

func newTestFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, id: strings.Repeat("a", 32), proof: strings.Repeat("b", 64)}
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
	f.server = httptest.NewUnstartedServer(nil)
	handler, err := newFixture("http://"+f.server.Listener.Addr().String(), f.slack.URL, testClient, testSecret, testApp, f.time)
	if err != nil {
		t.Fatal(err)
	}
	f.server.Config.Handler = handler
	f.server.Start()
	t.Cleanup(f.server.Close)
	return f
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
	app, team, token, scopes, enterprise := testApp, testWorkspace, testBot, botScopes, false
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
			if v.Binding == nil || v.Binding.AppID != testApp || v.Binding.WorkspaceID != testWorkspace ||
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
	f.now.Add(int64(pairingTTL / time.Second))
	f.install(install, "admin", http.StatusConflict)
	f.poll("reconnect_required")
	if f.identityExchanges.Load() != 1 || f.installExchanges.Load() != 0 {
		t.Fatal("invalid callbacks reached Slack token endpoints")
	}
	f.id = strings.Repeat("d", 32)
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
	f.request("POST", "/pairings/"+f.id+"/install", f.proof, nil, http.StatusConflict, nil)
	f.id = strings.Repeat("d", 32)
	f.poll("connecting")
	f.identity(f.start(signIn), "operator", http.StatusNoContent)
	f.install(f.start(installApp), "admin", http.StatusNoContent)
	f.poll("ready_to_link")
}

func TestOfficialPairingFixtureRejectsRemoteOrigins(t *testing.T) {
	for _, origin := range []string{"https://slack.com", "http://localhost:8080", "http://127.0.0.1:8080/api"} {
		if _, err := NewFixture("http://127.0.0.1:3210", origin, testClient, testSecret, testApp); err == nil {
			t.Errorf("accepted non-fixture Slack origin %q", origin)
		}
	}
}
