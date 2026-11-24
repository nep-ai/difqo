package slackintake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nep-ai/difqo/internal/store"
)

var oauthState = regexp.MustCompile(`^[0-9a-f]{64}$`)
var oauthClientID = regexp.MustCompile(`^[A-Za-z0-9.-]{1,128}$`)

func NewPilotConnections(tasks store.Tasks, origin, token string) (*Connections, error) {
	c, err := NewHTTPSConnections(tasks, origin)
	if err != nil {
		return nil, err
	}
	if token != "" && !oauthState.MatchString(token) {
		return nil, errors.New("DIFQO_SLACK_ENROLLMENT_TOKEN must contain 64 lowercase hex characters")
	}
	c.enrollmentToken = token
	return c, nil
}

func (c *Connections) enroll(ctx context.Context, p *store.SlackPairing) error {
	if p.Relay != c.origin || !pairingIDPattern.MatchString(p.ID) || !oauthState.MatchString(p.Proof) {
		return store.ErrSlackTransition
	}
	body, err := json.Marshal(map[string]string{"pairing_id": p.ID, "proof": p.Proof})
	if err != nil {
		return err
	}
	res, err := c.request(ctx, http.MethodPost, "/slack/enroll", c.enrollmentToken, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return relayChangeResult(res.StatusCode)
}

func relayChangeResult(status int) error {
	switch status {
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		return store.ErrSlackTransition
	default:
		return ErrRelayUnavailable
	}
}

func (c *Connections) revoke(ctx context.Context, p *store.SlackPairing) error {
	res, err := c.relayRequest(ctx, http.MethodDelete, p, "", nil)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized && c.enrollmentToken != "" && !p.RelayClaimed {
		// Begin may have failed before the relay registered the pairing.
		// Establish the same proof so DELETE can leave a durable tombstone.
		if err := c.enroll(ctx, p); err != nil {
			return err
		}
		return c.relayChange(ctx, http.MethodDelete, p, "", nil)
	}
	return relayChangeResult(res.StatusCode)
}

// OAuth returns only a validated Slack authorization URL for the current pairing.
func (c *Connections) OAuth(ctx context.Context, id, action string) (ConnectionView, error) {
	state, err := c.tasks.SlackConnection(ctx)
	if err != nil {
		return connectionView(state), err
	}
	p := state.Pairing
	if !strings.HasPrefix(c.origin, "https://") || state.Mode != "disconnected" ||
		p == nil || p.ID != id || p.Relay != c.origin || !time.Now().Before(p.Expires) ||
		(p.State != "connecting" && p.State != "approval_needed") {
		return connectionView(state), store.ErrSlackTransition
	}
	var suffix string
	switch action {
	case "start_identity":
		if p.Operator != "" {
			return connectionView(state), store.ErrSlackTransition
		}
		suffix = "/identity"
	case "start_install", "approval_needed":
		if p.Operator == "" || p.Installed != "" {
			return connectionView(state), store.ErrSlackTransition
		}
		suffix = "/install"
		if action == "approval_needed" {
			suffix = "/approval-needed"
		}
	default:
		return connectionView(state), store.ErrSlackTransition
	}
	if action == "approval_needed" {
		if err := c.relayChange(ctx, http.MethodPost, p, suffix, nil); err != nil {
			return connectionView(state), err
		}
		return c.Refresh(ctx, id)
	}
	raw, err := c.oauthURL(ctx, p, suffix)
	if err != nil {
		return connectionView(state), err
	}
	current, err := c.tasks.SlackConnection(ctx)
	if err != nil {
		return connectionView(current), err
	}
	if !samePairing(&current, state) || !time.Now().Before(p.Expires) {
		return connectionView(current), store.ErrSlackTransition
	}
	view := connectionView(current)
	view.OAuthURL = raw
	return view, nil
}

func (c *Connections) oauthURL(ctx context.Context, p *store.SlackPairing, suffix string) (string, error) {
	res, err := c.relayRequest(ctx, http.MethodPost, p, suffix, nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusConflict {
		return "", store.ErrSlackTransition
	}
	var reply struct{ URL string }
	body := &io.LimitedReader{R: res.Body, N: 8193}
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if res.StatusCode != http.StatusOK || decoder.Decode(&reply) != nil || decoder.Decode(new(any)) != io.EOF ||
		body.N == 0 || !c.validOAuthURL(reply.URL, p, suffix) {
		return "", ErrRelayUnavailable
	}
	return reply.URL, nil
}

func (c *Connections) validOAuthURL(raw string, p *store.SlackPairing, suffix string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "slack.com" || u.User != nil || u.RawPath != "" ||
		strings.Contains(raw, "#") {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || !oauthState.MatchString(q.Get("state")) || !oauthClientID.MatchString(q.Get("client_id")) ||
		q.Get("redirect_uri") != c.origin+"/oauth"+suffix || !idPattern.MatchString(q.Get("team")) ||
		(p.Workspace != "" && q.Get("team") != p.Workspace) {
		return false
	}
	for _, values := range q {
		if len(values) != 1 || strings.Contains(values[0], p.Proof) ||
			(c.enrollmentToken != "" && strings.Contains(values[0], c.enrollmentToken)) {
			return false
		}
	}
	if suffix == "/identity" {
		return u.Path == "/openid/connect/authorize" && len(q) == 8 && q.Get("scope") == "openid profile" &&
			q.Get("response_type") == "code" && q.Get("response_mode") == "form_post" && oauthState.MatchString(q.Get("nonce"))
	}
	return u.Path == "/oauth/v2/authorize" && len(q) == 5 && q.Get("scope") == "im:history,im:write,chat:write"
}
