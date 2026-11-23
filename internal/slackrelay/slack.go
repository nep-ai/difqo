package slackrelay

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

const botScopes = "im:history,im:write,chat:write"

var (
	workspaceID = regexp.MustCompile("^T[A-Z0-9]{1,63}$")
	operatorID  = regexp.MustCompile("^[UW][A-Z0-9]{1,63}$")
	dmID        = regexp.MustCompile("^D[A-Z0-9]{1,63}$")
	errSlack    = errors.New("Slack rejected the request or returned an invalid response")
	errRevoked  = errors.New("Slack installation is no longer authorized")
)

type slackClient struct {
	origin, clientID, clientSecret, appID string
	http                                  *http.Client
	verifier                              *oidc.IDTokenVerifier
}

type identity struct {
	Workspace string `json:"https://slack.com/team_id"`
	Operator  string `json:"https://slack.com/user_id"`
}

type installation struct {
	workspace, botToken string
}

func (s slackClient) authorize(kind flowKind, callback, state, nonce, team string) string {
	values := url.Values{"client_id": {s.clientID}, "state": {state}, "redirect_uri": {callback}}
	path := "/oauth/v2/authorize"
	if kind == signIn {
		path = "/openid/connect/authorize"
		values.Set("scope", "openid profile")
		values.Set("nonce", nonce)
		values.Set("response_type", "code")
		values.Set("response_mode", "form_post")
	} else {
		values.Set("scope", botScopes)
	}
	if team != "" {
		values.Set("team", team)
	}
	return s.origin + path + "?" + values.Encode()
}

func (s slackClient) exchangeIdentity(ctx context.Context, callback, code, nonce string) (identity, error) {
	var reply struct {
		OK      bool   `json:"ok"`
		IDToken string `json:"id_token"`
	}
	if err := s.post(ctx, "openid.connect.token", "", url.Values{
		"grant_type": {"authorization_code"}, "redirect_uri": {callback}, "code": {code},
		"client_id": {s.clientID}, "client_secret": {s.clientSecret},
	}, &reply); err != nil || !reply.OK {
		return identity{}, errSlack
	}
	token, err := s.verifier.Verify(ctx, reply.IDToken)
	if err != nil || len(token.Audience) != 1 || subtle.ConstantTimeCompare([]byte(token.Nonce), []byte(nonce)) != 1 {
		return identity{}, errSlack
	}
	var who identity
	if token.Claims(&who) != nil || !workspaceID.MatchString(who.Workspace) ||
		!operatorID.MatchString(who.Operator) || token.Subject != who.Operator {
		return identity{}, errSlack
	}
	return who, nil
}

func (s slackClient) exchangeInstall(ctx context.Context, callback, code string) (installation, error) {
	var reply struct {
		OK         bool                `json:"ok"`
		AppID      string              `json:"app_id"`
		Team       struct{ ID string } `json:"team"`
		OrgInstall bool                `json:"is_enterprise_install"`
		TokenType  string              `json:"token_type"`
		Token      string              `json:"access_token"`
		Scope      string              `json:"scope"`
	}
	if err := s.post(ctx, "oauth.v2.access", "", url.Values{
		"redirect_uri": {callback}, "code": {code},
	}, &reply); err != nil || !reply.OK || reply.AppID != s.appID || !workspaceID.MatchString(reply.Team.ID) ||
		reply.OrgInstall || reply.TokenType != "bot" || reply.Token == "" {
		return installation{}, errSlack
	}
	granted := strings.Split(reply.Scope, ",")
	for _, required := range strings.Split(botScopes, ",") {
		if !slices.Contains(granted, required) {
			return installation{}, errSlack
		}
	}
	return installation{workspace: reply.Team.ID, botToken: reply.Token}, nil
}

func (s slackClient) openDM(ctx context.Context, install installation, operator string) (string, error) {
	var reply struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Channel struct {
			ID   string `json:"id"`
			User string `json:"user"`
			IsIM bool   `json:"is_im"`
		} `json:"channel"`
	}
	if err := s.post(ctx, "conversations.open", install.botToken, url.Values{
		"users": {operator}, "return_im": {"true"},
	}, &reply); err != nil {
		return "", err
	}
	if !reply.OK && (reply.Error == "token_revoked" || reply.Error == "invalid_auth" || reply.Error == "missing_scope") {
		return "", errRevoked
	}
	if !reply.OK || !reply.Channel.IsIM || reply.Channel.User != operator || !dmID.MatchString(reply.Channel.ID) {
		return "", errSlack
	}
	return reply.Channel.ID, nil
}

func (s slackClient) post(ctx context.Context, method, token string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.origin+"/api/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return errSlack
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if method == "oauth.v2.access" {
		req.SetBasicAuth(s.clientID, s.clientSecret)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := s.http.Do(req)
	if err != nil {
		return errSlack
	}
	defer res.Body.Close()
	body := &io.LimitedReader{R: res.Body, N: 64<<10 + 1}
	dec := json.NewDecoder(body)
	if res.StatusCode != http.StatusOK || dec.Decode(out) != nil || dec.Decode(new(any)) != io.EOF || body.N == 0 {
		return errSlack
	}
	return nil
}
