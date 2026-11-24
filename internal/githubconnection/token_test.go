package githubconnection

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPilotTokenScope(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	proof := strings.Repeat("a1", 32)
	config := PilotConfig{
		AppID: 10, InstallationID: 20, RepositoryID: 30, OwnerID: 40,
		Repository: "fixture/repo", OwnerLogin: "fixture-owner",
		ProofSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(proof))),
		PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		})),
	}

	for _, tc := range []struct {
		name, auth, query, body string
		status, calls           int
	}{
		{name: "authorized", auth: proof, status: 200, calls: 3},
		{name: "wrong proof", auth: strings.Repeat("b2", 32), status: 401},
		{name: "caller selected repository", auth: proof, query: "?repository_id=99", status: 400},
		{name: "caller selected installation", auth: proof, body: `{"installation_id":99}`, status: 400},
		{name: "removed installation", auth: proof, status: 403, calls: 1},
		{name: "unexpected write grant", auth: proof, status: 403, calls: 1},
		{name: "expired token", auth: proof, status: 403, calls: 1},
		{name: "different repository", auth: proof, status: 403, calls: 2},
		{name: "multiple repositories", auth: proof, status: 403, calls: 2},
		{name: "revoked owner", auth: proof, status: 403, calls: 3},
		{name: "different owner", auth: proof, status: 403, calls: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch r.URL.Path {
				case "/app/installations/20/access_tokens":
					if r.Method != "POST" || !checkJWT(r.Header.Get("Authorization"), &key.PublicKey) {
						t.Error("invalid token issuance request")
					}
					var request struct {
						IDs         []int64           `json:"repository_ids"`
						Permissions map[string]string `json:"permissions"`
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil || !slices.Equal(request.IDs, []int64{30}) ||
						len(request.Permissions) != 3 || request.Permissions["issues"] != "read" ||
						request.Permissions["pull_requests"] != "read" || request.Permissions["metadata"] != "read" {
						t.Error("request did not pin repository ID and read permissions")
					}
					if tc.name == "removed installation" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					permission := "read"
					if tc.name == "unexpected write grant" {
						permission = "write"
					}
					expires := time.Now().Add(time.Hour)
					if tc.name == "expired token" {
						expires = time.Now().Add(-time.Minute)
					}
					w.WriteHeader(http.StatusCreated)
					fmt.Fprintf(w, `{"token":"fixture-token","expires_at":%q,"permissions":{"issues":%q,"pull_requests":"read","metadata":"read"}}`,
						expires.UTC().Format(time.RFC3339), permission)
				case "/installation/repositories":
					checkRead(t, r)
					count, id := 1, 30
					if tc.name == "different repository" {
						id = 31
					}
					if tc.name == "multiple repositories" {
						count = 2
					}
					fmt.Fprintf(w, `{"total_count":%d,"repositories":[{"id":%d,"full_name":"fixture/repo"}]}`, count, id)
				case "/repos/fixture/repo/collaborators/fixture-owner/permission":
					checkRead(t, r)
					permission, owner := "read", 40
					if tc.name == "revoked owner" {
						permission = "none"
					}
					if tc.name == "different owner" {
						owner = 41
					}
					fmt.Fprintf(w, `{"permission":%q,"user":{"id":%d}}`, permission, owner)
				default:
					t.Error("unexpected upstream path")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer github.Close()
			handler, err := newPilot(config, github.URL, github.Client())
			if err != nil {
				t.Fatal(err)
			}
			service := httptest.NewTLSServer(handler)
			defer service.Close()
			if tc.name == "authorized" {
				grant, err := fetch(t.Context(), service.Client(), service.URL, proof)
				if err != nil || grant.Repository != "fixture/repo" || grant.RepositoryID != 30 ||
					grant.OwnerID != 40 || grant.Token != "fixture-token" {
					t.Fatal("approved connection did not get its scoped grant")
				}
			} else {
				res := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, TokenPath+tc.query, strings.NewReader(tc.body))
				req.Header.Set("Authorization", "Bearer "+tc.auth)
				handler.ServeHTTP(res, req)
				if res.Code != tc.status || res.Body.Len() != 0 || res.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("expected HTTP %d without credential data, got %d", tc.status, res.Code)
				}
			}
			if int(calls.Load()) != tc.calls {
				t.Fatalf("expected %d upstream requests, got %d", tc.calls, calls.Load())
			}
		})
	}
}

func checkRead(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-token" {
		t.Error("expected a read-only request with the installation credential")
	}
}

func checkJWT(auth string, key *rsa.PublicKey) bool {
	value, ok := strings.CutPrefix(auth, "Bearer ")
	parts := strings.Split(value, ".")
	if !ok || len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(strings.Join(parts[:2], ".")))
	if err != nil || rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Issuer string `json:"iss"`
		Issued int64  `json:"iat"`
		Expiry int64  `json:"exp"`
	}
	now := time.Now().Unix()
	return err == nil && json.Unmarshal(raw, &claims) == nil && claims.Issuer == "10" &&
		claims.Issued < now && claims.Expiry > now && claims.Expiry-claims.Issued <= 600
}

func TestCredentialRequestsDoNotFollowRedirects(t *testing.T) {
	for _, hop := range []string{"Connections", "GitHub"} {
		t.Run(hop, func(t *testing.T) {
			var destinations atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect-destination" {
					destinations.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("redirect destination received credentials")
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Header.Get("Authorization") == "" {
					t.Error("initial request did not carry fixture credentials")
				}
				w.Header().Set("Location", "/redirect-destination")
				w.WriteHeader(http.StatusTemporaryRedirect)
			})
			client := httpClient()
			if hop == "Connections" {
				upstream := httptest.NewTLSServer(handler)
				defer upstream.Close()
				client.Transport = upstream.Client().Transport
				if _, err := fetch(t.Context(), client, upstream.URL, strings.Repeat("a1", 32)); err == nil {
					t.Fatal("broker redirect was accepted")
				}
			} else {
				upstream := httptest.NewServer(handler)
				defer upstream.Close()
				if err := githubRequest(t.Context(), client, http.MethodPost, upstream.URL, "fixture-jwt", []byte("{}"), http.StatusCreated, new(Grant)); err == nil {
					t.Fatal("GitHub redirect was accepted")
				}
			}
			if destinations.Load() != 0 {
				t.Fatal("request followed a redirect")
			}
		})
	}
}

func TestFetchRejectsUnsafeBrokerOrigins(t *testing.T) {
	var requests atomic.Int32
	broker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer broker.Close()

	for _, origin := range []string{
		"http://" + strings.TrimPrefix(broker.URL, "https://"),
		broker.URL + "/relay",
		broker.URL + "/%2f",
		broker.URL + "?repository=elsewhere",
		broker.URL + "#fragment",
	} {
		if _, err := fetch(t.Context(), broker.Client(), origin, strings.Repeat("a1", 32)); err == nil ||
			!strings.Contains(err.Error(), "requires a Connections HTTPS origin") {
			t.Errorf("invalid origin %q did not fail validation", origin)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid origin received a request")
	}
}
