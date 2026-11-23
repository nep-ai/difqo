// Package slackrelay implements the official Slack pairing protocol for loopback fixtures.
package slackrelay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/nep-ai/difqo/internal/store"
)

const pairingTTL = 15 * time.Minute

type flowKind string

const (
	signIn     flowKind = "identity"
	installApp flowKind = "install"
)

type flow struct {
	pairing *pairing
	kind    flowKind
	nonce   string
	expires time.Time
	used    bool
}

type pairing struct {
	proof            [sha256.Size]byte
	expires          time.Time
	identity         identity
	install          installation
	channel          string
	pending, revoked bool
	flows            map[flowKind]string
}

type pairingStatus struct {
	State     string              `json:"state"`
	Workspace string              `json:"verified_workspace,omitempty"`
	Operator  string              `json:"verified_operator,omitempty"`
	Installed string              `json:"installed_workspace,omitempty"`
	Binding   *store.SlackBinding `json:"binding,omitempty"`
}

type relay struct {
	mu       sync.Mutex
	pairings map[string]*pairing
	flows    map[string]*flow
	origin   string
	slack    slackClient
	now      func() time.Time
}

var (
	pairingID  = regexp.MustCompile("^[0-9a-f]{32}$")
	proofID    = regexp.MustCompile("^[0-9a-f]{64}$")
	slackAppID = regexp.MustCompile("^A[A-Z0-9]{1,63}$")
)

// NewFixture builds an HTTP handler with a numeric loopback Slack simulator
// and in-memory pairing state. It is deliberately unwired from the application.
func NewFixture(origin, slackOrigin, clientID, clientSecret, appID string) (http.Handler, error) {
	return newFixture(origin, slackOrigin, clientID, clientSecret, appID, time.Now)
}

func newFixture(origin, slackOrigin, clientID, clientSecret, appID string, now func() time.Time) (http.Handler, error) {
	if !loopbackOrigin(origin) || !loopbackOrigin(slackOrigin) || clientID == "" || clientSecret == "" || !slackAppID.MatchString(appID) {
		return nil, errors.New("Slack relay fixture requires numeric HTTP loopback origins and synthetic app configuration")
	}
	client := &http.Client{
		Transport:     &http.Transport{},
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), slackOrigin+"/openid/connect/keys")
	r := &relay{
		origin: origin, now: now, pairings: make(map[string]*pairing), flows: make(map[string]*flow),
		slack: slackClient{origin: slackOrigin, clientID: clientID, clientSecret: clientSecret, appID: appID, http: client,
			verifier: oidc.NewVerifier(slackOrigin, keys, &oidc.Config{ClientID: clientID,
				SupportedSigningAlgs: []string{oidc.RS256}, Now: now})},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /pairings/{id}", r.poll)
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

// access requires the relay mutex. PUT is the only route that can register an ID.
func (r *relay) access(w http.ResponseWriter, req *http.Request, register bool) *pairing {
	id := req.PathValue("id")
	proof, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !ok || !pairingID.MatchString(id) || !proofID.MatchString(proof) {
		w.WriteHeader(http.StatusUnauthorized)
		return nil
	}
	hash := sha256.Sum256([]byte(proof))
	p := r.pairings[id]
	if p == nil && register {
		now := r.now()
		for _, prior := range r.pairings {
			if !now.Before(prior.expires) {
				r.clear(prior)
			}
		}
		p = &pairing{proof: hash, expires: now.Add(pairingTTL), flows: make(map[flowKind]string)}
		r.pairings[id] = p
	}
	if p == nil || subtle.ConstantTimeCompare(p.proof[:], hash[:]) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return nil
	}
	return p
}

func (r *relay) poll(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	p := r.access(w, req, true)
	if p == nil {
		r.mu.Unlock()
		return
	}
	who, installed := p.identity, p.install
	resolve := r.now().Before(p.expires) && !p.revoked && p.channel == "" &&
		who.Workspace != "" && who.Workspace == installed.workspace
	r.mu.Unlock()

	if resolve {
		channel, err := r.slack.openDM(req.Context(), installed, who.Operator)
		r.mu.Lock()
		if r.now().Before(p.expires) && !p.revoked && p.install == installed && p.identity == who {
			if errors.Is(err, errRevoked) {
				p.revoked = true
			} else if err == nil {
				p.channel = channel
			}
		}
		r.mu.Unlock()
	}

	r.mu.Lock()
	status := r.status(p)
	r.mu.Unlock()
	writeJSON(w, status)
}

// status requires the relay mutex.
func (r *relay) status(p *pairing) pairingStatus {
	if p.revoked || !r.now().Before(p.expires) {
		r.clear(p)
		return pairingStatus{State: "reconnect_required"}
	}
	v := pairingStatus{State: "connecting", Workspace: p.identity.Workspace, Operator: p.identity.Operator, Installed: p.install.workspace}
	switch {
	case p.channel != "":
		v.State = "ready_to_link"
		v.Binding = &store.SlackBinding{AppID: r.slack.appID, WorkspaceID: v.Workspace, OperatorID: v.Operator, ChannelID: p.channel}
	case v.Installed != "":
		v.State = "installed"
	case p.pending:
		v.State = "approval_needed"
	}
	return v
}

// clear releases provider data while retaining proof ownership of a terminal pairing.
func (r *relay) clear(p *pairing) {
	for _, state := range p.flows {
		delete(r.flows, state)
	}
	*p = pairing{proof: p.proof, expires: p.expires, revoked: p.revoked}
}

func (r *relay) markPending(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.access(w, req, false)
	if p == nil {
		return
	}
	if !r.now().Before(p.expires) || p.revoked || p.identity.Workspace == "" ||
		p.install.workspace != "" || p.flows[installApp] == "" {
		w.WriteHeader(http.StatusConflict)
		return
	}
	p.pending = true
	w.WriteHeader(http.StatusNoContent)
}

func (r *relay) start(kind flowKind) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		p := r.access(w, req, false)
		if p == nil {
			return
		}
		if !r.now().Before(p.expires) || p.revoked ||
			(kind == signIn && p.identity.Workspace != "") || (kind == installApp && p.install.workspace != "") {
			w.WriteHeader(http.StatusConflict)
			return
		}
		delete(r.flows, p.flows[kind])
		state, nonce := randomSecret(), ""
		if kind == signIn {
			nonce = randomSecret()
		}
		r.flows[state] = &flow{pairing: p, kind: kind, nonce: nonce, expires: p.expires}
		p.flows[kind] = state
		team := p.identity.Workspace
		if kind == signIn {
			team = p.install.workspace
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
		r.mu.Lock()
		f := r.flows[state]
		if f == nil || f.kind != kind || f.used || !r.now().Before(f.expires) || f.pairing.revoked {
			r.mu.Unlock()
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.used = true
		r.mu.Unlock()

		if denied != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		result := r.complete(req, f, state, code)
		w.WriteHeader(result)
	}
}

func (r *relay) complete(req *http.Request, f *flow, state, code string) int {
	var who identity
	var installed installation
	var err error
	callback := r.callbackURL(f.kind)
	if f.kind == signIn {
		who, err = r.slack.exchangeIdentity(req.Context(), callback, code, f.nonce)
	} else {
		installed, err = r.slack.exchangeInstall(req.Context(), callback, code)
	}
	if err != nil {
		return http.StatusBadGateway
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	p := f.pairing
	if p.flows[f.kind] != state || !r.now().Before(f.expires) || p.revoked {
		return http.StatusConflict
	}
	if f.kind == signIn {
		p.identity = who
		if p.install.workspace != who.Workspace {
			p.install = installation{}
		}
	} else {
		if p.identity.Workspace != "" && installed.workspace != p.identity.Workspace {
			return http.StatusConflict
		}
		p.install, p.pending = installed, false
	}
	return http.StatusNoContent
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
