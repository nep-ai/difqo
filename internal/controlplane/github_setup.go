package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nep-ai/difqo/internal/githubconnection"
	"github.com/nep-ai/difqo/internal/store"
)

type gitHubEnrollment struct {
	Repository string                 `json:"repository"`
	Enrollment store.GitHubEnrollment `json:"enrollment"`
	Proof      string                 `json:"proof"`
}

func registerGitHubSetup(router *http.ServeMux, tasks store.Tasks, origin, file string,
	poll func(context.Context, store.GitHubEnrollment, githubconnection.Grant) (int, error)) {
	var operation sync.Mutex
	type connectionView struct {
		Repository string `json:"repository,omitempty"`
		Imported   *int   `json:"imported,omitempty"`
	}

	respond := func(w http.ResponseWriter, imported *int) {
		w.Header().Set("Cache-Control", "no-store")
		current, err := readGitHubEnrollment(file)
		if err != nil {
			http.Error(w, "Couldn’t read the saved GitHub connection.", http.StatusInternalServerError)
			return
		}
		if current.Proof == "" {
			current.Repository = ""
		}
		writeJSON(w, http.StatusOK, connectionView{Repository: current.Repository, Imported: imported})
	}
	router.HandleFunc("GET /setup/github", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, nil)
	})
	router.HandleFunc("POST /setup/github", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var in struct {
			Action string `json:"action"`
			Code   string `json:"code"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Action != "connect" && in.Action != "sync" && in.Action != "disconnect" {
			http.Error(w, "Unknown GitHub connection action.", http.StatusBadRequest)
			return
		}
		if !operation.TryLock() {
			http.Error(w, "Another GitHub connection action is in progress.", http.StatusConflict)
			return
		}
		defer operation.Unlock()

		enrollment, err := readGitHubEnrollment(file)
		if err != nil {
			http.Error(w, "Couldn’t read the saved GitHub connection.", http.StatusInternalServerError)
			return
		}
		if in.Action == "disconnect" {
			if enrollment.Proof != "" {
				enrollment.Proof = ""
				if err := saveGitHubEnrollment(file, enrollment); err != nil {
					http.Error(w, "Couldn’t remove the local GitHub connection.", http.StatusInternalServerError)
					return
				}
			}
			respond(w, nil)
			return
		}
		proof := enrollment.Proof
		if in.Action == "connect" {
			proof = strings.TrimSpace(in.Code)
		}
		if proof == "" {
			http.Error(w, "Enter your GitHub pilot connection code.", http.StatusBadRequest)
			return
		}
		// A single import can take longer than the general loopback write timeout.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(56 * time.Second))
		ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
		defer cancel()
		start := time.Now().UTC().Truncate(time.Second)
		grant, err := githubconnection.Fetch(ctx, origin, proof)
		if err != nil {
			http.Error(w, "Couldn’t verify GitHub access. Check your pilot code and confirm the official app still has access to the approved repository.", http.StatusBadGateway)
			return
		}
		sameScope := enrollment.Repository == grant.Repository &&
			enrollment.Enrollment.RepositoryID == grant.RepositoryID && enrollment.Enrollment.OwnerID == grant.OwnerID
		if in.Action == "connect" {
			if sameScope {
				start = enrollment.Enrollment.Start
			} else {
				start, err = tasks.GitHubEnrollmentStart(ctx, grant.RepositoryID, grant.OwnerID, start)
				if err != nil {
					storeError(w, err)
					return
				}
			}
			err := saveGitHubEnrollment(file, gitHubEnrollment{
				Repository: grant.Repository, Proof: proof,
				Enrollment: store.GitHubEnrollment{RepositoryID: grant.RepositoryID, OwnerID: grant.OwnerID, Start: start},
			})
			if err != nil {
				http.Error(w, "Couldn’t save the GitHub connection.", http.StatusInternalServerError)
				return
			}
			respond(w, nil)
			return
		}
		if !sameScope {
			http.Error(w, "The approved GitHub repository or account changed. Reconnect GitHub to confirm the new scope.", http.StatusConflict)
			return
		}
		count, err := poll(ctx, enrollment.Enrollment, grant)
		if err != nil {
			http.Error(w, "Couldn’t sync GitHub mentions. Check repository access and try again.", http.StatusBadGateway)
			return
		}
		respond(w, &count)
	})
}

func readGitHubEnrollment(file string) (gitHubEnrollment, error) {
	var enrollment gitHubEnrollment
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return enrollment, nil
	}
	if err != nil {
		return enrollment, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > 4096 {
		return enrollment, errors.New("invalid local GitHub connection file")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return enrollment, err
	}
	err = json.Unmarshal(raw, &enrollment)
	return enrollment, err
}

func saveGitHubEnrollment(file string, enrollment gitHubEnrollment) error {
	temp, err := os.CreateTemp(filepath.Dir(file), ".github-connection-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	err = json.NewEncoder(temp).Encode(enrollment)
	closeErr := temp.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	return os.Rename(temp.Name(), file)
}
