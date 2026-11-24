// Package githubconnection brokers scoped tokens for the official GitHub pilot.
package githubconnection

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const TokenPath = "/github/token"

var handle = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
var repository = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+$`)

type Grant struct {
	Repository   string    `json:"repository"`
	RepositoryID int64     `json:"repository_id"`
	OwnerID      int64     `json:"owner_id"`
	Token        string    `json:"token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type PilotConfig struct {
	AppID, InstallationID, RepositoryID, OwnerID int64
	Repository, OwnerLogin                       string
	PrivateKeyPEM, ProofSHA256                   string
}

type pilot struct {
	config PilotConfig
	key    *rsa.PrivateKey
	proof  []byte
	client *http.Client
	api    string
}

func NewPilot(config PilotConfig) (http.Handler, error) {
	return newPilot(config, "https://api.github.com", httpClient())
}

func newPilot(config PilotConfig, api string, client *http.Client) (http.Handler, error) {
	if !validRepository(config.Repository) || !handle.MatchString(config.OwnerLogin) ||
		config.AppID <= 0 || config.InstallationID <= 0 || config.RepositoryID <= 0 || config.OwnerID <= 0 {
		return nil, errors.New("GitHub pilot requires app, installation, repository and owner IDs, repository name and owner login")
	}
	proof, err := hex.DecodeString(config.ProofSHA256)
	if err != nil || len(proof) != sha256.Size {
		return nil, errors.New("DIFQO_GITHUB_PILOT_PROOF_SHA256 must be a SHA-256 hex digest")
	}
	block, rest := pem.Decode([]byte(config.PrivateKeyPEM))
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("GitHub pilot requires an RSA private key in PEM format")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
		if parseErr == nil {
			key, _ = parsed.(*rsa.PrivateKey)
		}
	}
	if key == nil || key.N.BitLen() < 2048 || key.Validate() != nil {
		return nil, errors.New("GitHub pilot requires a valid RSA private key of at least 2048 bits")
	}
	config.PrivateKeyPEM, config.ProofSHA256 = "", ""
	return (&pilot{config: config, key: key, proof: proof, api: api, client: client}).handler(), nil
}

func (p *pilot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+TokenPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		proof := sha256.Sum256([]byte(token))
		if !ok || !validProof(token) || subtle.ConstantTimeCompare(proof[:], p.proof) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.ContentLength != 0 || r.URL.RawQuery != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		grant, err := p.issue(ctx)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, errScope) {
				status = http.StatusForbidden
			}
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(grant)
	})
	return mux
}

var errScope = errors.New("GitHub installation or owner no longer has the enrolled access")

func (p *pilot) issue(ctx context.Context) (Grant, error) {
	var token struct {
		Token       string            `json:"token"`
		ExpiresAt   time.Time         `json:"expires_at"`
		Permissions map[string]string `json:"permissions"`
	}
	jwt, err := p.appJWT(time.Now())
	if err != nil {
		return Grant{}, err
	}
	config := p.config
	input, _ := json.Marshal(struct {
		RepositoryIDs []int64           `json:"repository_ids"`
		Permissions   map[string]string `json:"permissions"`
	}{[]int64{config.RepositoryID}, map[string]string{"issues": "read", "pull_requests": "read", "metadata": "read"}})
	address := fmt.Sprintf("%s/app/installations/%d/access_tokens", p.api, config.InstallationID)
	if err := githubRequest(ctx, p.client, http.MethodPost, address, jwt, input, http.StatusCreated, &token); err != nil {
		return Grant{}, err
	}
	if !validToken(token.Token, token.ExpiresAt) || token.Permissions["issues"] != "read" ||
		token.Permissions["pull_requests"] != "read" || token.Permissions["metadata"] != "read" || len(token.Permissions) != 3 {
		return Grant{}, errScope
	}

	var scope struct {
		Count int `json:"total_count"`
		Repos []struct {
			ID   int64  `json:"id"`
			Name string `json:"full_name"`
		} `json:"repositories"`
	}
	if err := githubRequest(ctx, p.client, http.MethodGet, p.api+"/installation/repositories?per_page=2", token.Token, nil, http.StatusOK, &scope); err != nil {
		return Grant{}, err
	}
	if scope.Count != 1 || len(scope.Repos) != 1 || scope.Repos[0].ID != config.RepositoryID ||
		!strings.EqualFold(scope.Repos[0].Name, config.Repository) {
		return Grant{}, errScope
	}

	var access struct {
		Permission string `json:"permission"`
		User       struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	address = p.api + "/repos/" + config.Repository + "/collaborators/" + config.OwnerLogin + "/permission"
	if err := githubRequest(ctx, p.client, http.MethodGet, address, token.Token, nil, http.StatusOK, &access); err != nil {
		return Grant{}, err
	}
	switch access.Permission {
	case "read", "write", "admin":
	default:
		return Grant{}, errScope
	}
	if access.User.ID != config.OwnerID {
		return Grant{}, errScope
	}
	return Grant{Repository: scope.Repos[0].Name, RepositoryID: config.RepositoryID, OwnerID: config.OwnerID,
		Token: token.Token, ExpiresAt: token.ExpiresAt}, nil
}

func (p *pilot) appJWT(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(struct {
		Issuer string `json:"iss"`
		Issued int64  `json:"iat"`
		Expiry int64  `json:"exp"`
	}{strconv.FormatInt(p.config.AppID, 10), now.Add(-time.Minute).Unix(), now.Add(5 * time.Minute).Unix()})
	message := header + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return message + "." + enc.EncodeToString(signature), nil
}

func Fetch(ctx context.Context, origin, proof string) (Grant, error) {
	return fetch(ctx, httpClient(), origin, proof)
}

func fetch(ctx context.Context, client *http.Client, origin, proof string) (Grant, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" ||
		u.ForceQuery || strings.Contains(origin, "#") || !validProof(proof) {
		return Grant{}, errors.New("official GitHub intake requires a Connections HTTPS origin and a 32-byte hex connection proof")
	}
	var grant Grant
	err = githubRequest(ctx, client, http.MethodPost, "https://"+u.Host+TokenPath, proof, nil, http.StatusOK, &grant)
	if errors.Is(err, errScope) {
		return Grant{}, errors.New("official GitHub connection is unauthorized or access was revoked; reconnect the pilot")
	}
	if err != nil || !validToken(grant.Token, grant.ExpiresAt) || !validRepository(grant.Repository) || grant.RepositoryID <= 0 || grant.OwnerID <= 0 {
		return Grant{}, errors.New("could not obtain a scoped token from the official GitHub connection")
	}
	return grant, nil
}

func validRepository(name string) bool {
	_, repo, _ := strings.Cut(name, "/")
	return repository.MatchString(name) && repo != "." && repo != ".."
}

func validProof(proof string) bool {
	decoded, err := hex.DecodeString(proof)
	return err == nil && len(decoded) == 32
}

func validToken(token string, expires time.Time) bool {
	now := time.Now()
	return len(token) > 0 && len(token) <= 4096 && strings.IndexFunc(token, func(r rune) bool {
		return r <= ' ' || r > '~'
	}) < 0 && expires.After(now.Add(30*time.Second)) && expires.Before(now.Add(61*time.Minute))
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func githubRequest(ctx context.Context, client *http.Client, method, address, token string, body []byte, status int, dst any) error {
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return errors.New("could not prepare GitHub connection request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("GitHub connection request failed")
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity:
		return errScope
	case status:
		const limit = 128 << 10
		data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
		if err == nil && len(data) <= limit && json.Unmarshal(data, dst) == nil {
			return nil
		}
	}
	return errors.New("GitHub connection returned an unexpected response")
}
