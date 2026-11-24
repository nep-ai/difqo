// Package slackrelay implements the official Slack pairing and event protocols for loopback fixtures.
package slackrelay

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nep-ai/difqo/internal/store"
)

type flowKind string

const (
	signIn     flowKind = "identity"
	installApp flowKind = "install"
)

type pairingStatus struct {
	State     string              `json:"state"`
	Workspace string              `json:"verified_workspace,omitempty"`
	Operator  string              `json:"verified_operator,omitempty"`
	Installed string              `json:"installed_workspace,omitempty"`
	Binding   *store.SlackBinding `json:"binding,omitempty"`
}

type relay struct {
	pairings pairingStore
	origin   string
	slack    slackClient
}

var (
	pairingID  = regexp.MustCompile("^[0-9a-f]{32}$")
	proofID    = regexp.MustCompile("^[0-9a-f]{64}$")
	slackAppID = regexp.MustCompile("^A[A-Z0-9]{1,63}$")
)

// NewFixture builds an HTTP handler for a numeric loopback Slack simulator
// and a dedicated relay database. The synthetic token key must survive restarts.
func NewFixture(db *pgxpool.Pool, tokenKey [32]byte, origin, slackOrigin, clientID, clientSecret, appID, signingSecret string) (http.Handler, error) {
	return newFixture(db, tokenKey, origin, slackOrigin, clientID, clientSecret, appID, signingSecret, time.Now)
}

func newFixture(db *pgxpool.Pool, tokenKey [32]byte, origin, slackOrigin, clientID, clientSecret, appID, signingSecret string, now func() time.Time) (http.Handler, error) {
	if !loopbackOrigin(origin) || !loopbackOrigin(slackOrigin) || clientID == "" || clientSecret == "" || signingSecret == "" || !slackAppID.MatchString(appID) {
		return nil, errors.New("Slack relay fixture requires numeric HTTP loopback origins and synthetic app configuration")
	}
	if db == nil || tokenKey == ([32]byte{}) {
		return nil, errors.New("Slack relay fixture requires a relay database and synthetic token key")
	}
	block, err := aes.NewCipher(tokenKey[:])
	if err != nil {
		return nil, err
	}
	tokens, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport:     &http.Transport{},
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), slackOrigin+"/openid/connect/keys")
	r := &relay{
		origin: origin, pairings: pairingStore{db: db, appID: appID, tokens: tokens},
		slack: slackClient{origin: slackOrigin, clientID: clientID, clientSecret: clientSecret, appID: appID, http: client,
			verifier: oidc.NewVerifier(slackOrigin, keys, &oidc.Config{ClientID: clientID,
				SupportedSigningAlgs: []string{oidc.RS256}, Now: now})},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /pairings/{id}", r.poll)
	mux.HandleFunc("DELETE /pairings/{id}", r.revoke)
	mux.HandleFunc("POST /pairings/{id}/claim", r.claim)
	mux.HandleFunc("GET /pairings/{id}/events", r.deliveries)
	mux.HandleFunc("PUT /pairings/{id}/events/{event}", r.receipt)
	mux.HandleFunc("POST /slack/events", r.signedEvents(signingSecret, now))
	mux.HandleFunc("POST /pairings/{id}/identity", r.start(signIn))
	mux.HandleFunc("POST /pairings/{id}/install", r.start(installApp))
	mux.HandleFunc("POST /pairings/{id}/approval-needed", r.markPending)
	mux.HandleFunc("POST /oauth/identity", r.callback(signIn))
	mux.HandleFunc("GET /oauth/install", r.callback(installApp))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, req)
	}), nil
}

func loopbackOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Port() == "" || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.IsLoopback()
}

func pairingAuth(req *http.Request) (id, proof string, ok bool) {
	id = req.PathValue("id")
	proof, ok = strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	return id, proof, ok && pairingID.MatchString(id) && proofID.MatchString(proof)
}

func pairingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPairingAccess):
		w.WriteHeader(http.StatusUnauthorized)
	case errors.Is(err, errPairingState):
		w.WriteHeader(http.StatusConflict)
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

func (r *relay) poll(w http.ResponseWriter, req *http.Request) {
	id, proof, ok := pairingAuth(req)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := req.Context()
	p, err := r.pairings.read(ctx, id, proof)
	if err != nil {
		pairingError(w, err)
		return
	}
	if !p.terminal() && p.channel == "" && p.identity.Workspace != "" && p.identity.Workspace == p.workspace {
		token, err := r.pairings.tokens.Open(nil, nil, p.sealedToken, r.pairings.tokenScope(id, p.workspace))
		if err != nil {
			pairingError(w, err)
			return
		}
		channel, dmErr := r.slack.openDM(ctx, installation{workspace: p.workspace, botToken: string(token)}, p.identity.Operator)
		revoked := errors.Is(dmErr, errRevoked)
		if dmErr == nil || revoked {
			if err := r.pairings.finishDM(ctx, id, proof, channel, revoked); err != nil {
				pairingError(w, err)
				return
			}
		}
		p, err = r.pairings.read(ctx, id, proof)
		if err != nil {
			pairingError(w, err)
			return
		}
	}
	writeJSON(w, r.status(p))
}

func (r *relay) status(p pairing) pairingStatus {
	if p.terminal() {
		return pairingStatus{State: "reconnect_required"}
	}
	v := pairingStatus{State: "connecting", Workspace: p.identity.Workspace, Operator: p.identity.Operator, Installed: p.workspace}
	switch {
	case p.channel != "":
		v.State = "ready_to_link"
		binding := r.pairings.binding(p)
		v.Binding = &binding
	case v.Installed != "":
		v.State = "installed"
	case p.pending:
		v.State = "approval_needed"
	}
	return v
}

func (r *relay) revoke(w http.ResponseWriter, req *http.Request) {
	r.change(w, req, r.pairings.revoke)
}

func (r *relay) markPending(w http.ResponseWriter, req *http.Request) {
	r.change(w, req, r.pairings.pending)
}

func (r *relay) change(w http.ResponseWriter, req *http.Request, operation func(context.Context, string, string) error) {
	id, proof, ok := pairingAuth(req)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err := operation(req.Context(), id, proof); err != nil {
		pairingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *relay) start(kind flowKind) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, proof, ok := pairingAuth(req)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		state, nonce := randomSecret(), ""
		if kind == signIn {
			nonce = randomSecret()
		}
		team, err := r.pairings.start(req.Context(), id, proof, kind, state, nonce)
		if err != nil {
			pairingError(w, err)
			return
		}
		writeJSON(w, map[string]string{"url": r.slack.authorize(kind, r.callbackURL(kind), state, nonce, team)})
	}
}

func (r *relay) callback(kind flowKind) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodHead {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		source, err := url.ParseQuery(req.URL.RawQuery)
		if kind == signIn {
			req.Body = http.MaxBytesReader(w, req.Body, 8<<10)
			err = req.ParseForm()
			source = req.PostForm
		}
		state, code, denied := source.Get("state"), source.Get("code"), source.Get("error")
		if err != nil || !proofID.MatchString(state) || len(source["state"]) != 1 ||
			len(source["code"]) > 1 || len(source["error"]) > 1 || (code == "") == (denied == "") || len(code) > 4096 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx := req.Context()
		f, err := r.pairings.consume(ctx, kind, state)
		if err != nil {
			pairingError(w, err)
			return
		}
		if denied != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var who identity
		var installed installation
		callback := r.callbackURL(kind)
		if kind == signIn {
			who, err = r.slack.exchangeIdentity(ctx, callback, code, f.nonce)
		} else {
			installed, err = r.slack.exchangeInstall(ctx, callback, code)
		}
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if err := r.pairings.complete(ctx, f, who, installed); err != nil {
			pairingError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (r *relay) callbackURL(kind flowKind) string {
	return r.origin + "/oauth/" + string(kind)
}

func randomSecret() string {
	var bytes [32]byte
	rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
