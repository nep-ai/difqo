package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nep-ai/difqo/internal/store"
)

func TestCodexSetupDevicePrompt(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "fake-helper")
	proceed := filepath.Join(dir, "proceed")
	if err := os.WriteFile(helper, []byte(`printf '\033[94mhttps://auth.openai.com/codex/device\033[0m\n\n'
printf '2. Enter this one-time code (expires in 15 minutes)\n'
printf ' ABCD-EFGHI\n'
while [ ! -e "$(dirname "$0")/proceed" ]; do sleep 0.02; done
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile(proceed, nil, 0o600) })

	router := http.NewServeMux()
	registerCodexSetup(router, helper, store.Tasks{})
	call := func(method, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/setup/codex", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}
	if got := call(http.MethodPost, `{"action":"prepare"}`); got.Code != http.StatusAccepted {
		t.Fatalf("start sign-in returned %d: %s", got.Code, got.Body)
	}
	waitFor := func(state string) codexSetupView {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			response := call(http.MethodGet, "")
			var view codexSetupView
			if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
				t.Fatal(err)
			}
			if view.State == state {
				if response.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("setup response must not be cached")
				}
				return view
			}
		}
		t.Fatalf("setup did not report %s", state)
		return codexSetupView{}
	}
	if view := waitFor("authorize"); view.URL != deviceSignInURL || view.Code != "ABCD-EFGHI" {
		t.Fatalf("invalid device prompt: %+v", view)
	}
	if got := call(http.MethodPost, `{"action":"prepare"}`); got.Code != http.StatusConflict {
		t.Fatalf("concurrent sign-in returned %d", got.Code)
	}
	if err := os.WriteFile(proceed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if view := waitFor("ready"); view.URL != "" || view.Code != "" {
		t.Fatalf("successful setup retained the device challenge: %+v", view)
	}
}
