package controlplane

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/execution"

	"github.com/nep-ai/difqo/internal/store"
)

var artifactSuffix = regexp.MustCompile(`^[0-9]+$`)

func Handler(tasks store.Tasks, web fs.FS, artifacts *os.Root, projects []store.SpecialistProject) http.Handler {
	router := http.NewServeMux()
	registerSpecialists(router, store.Specialists{DB: tasks.DB, Projects: projects})
	registerDecisions(router, tasks)

	if web != nil {
		router.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, web, "index.html")
		})
		router.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, web, "favicon.svg")
		})
		for _, directory := range []string{"assets", "licenses"} {
			router.HandleFunc("GET /"+directory+"/{file}", func(w http.ResponseWriter, r *http.Request) {
				filename := r.PathValue("file")
				if filename == "." || filename == ".." || strings.ContainsAny(filename, "/\\") {
					http.NotFound(w, r)
					return
				}
				http.ServeFileFS(w, r, web, directory+"/"+filename)
			})
		}
	}
	router.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
		recent, err := tasks.Recent(r.Context())
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, recent)
	})
	if artifacts != nil {
		registerReview(router, tasks, artifacts)
		router.HandleFunc("GET /tasks/{id}/evidence/{file}", func(w http.ResponseWriter, r *http.Request) {
			id, ok := readID(w, r)
			if !ok {
				return
			}
			filename := r.PathValue("file")
			switch filename {
			case "change.diff", "check.log", "execution.log":
			default:
				http.NotFound(w, r)
				return
			}
			task, err := tasks.Get(r.Context(), id)
			if err != nil {
				storeError(w, err)
				return
			}
			if task.Attempt == nil || task.Attempt.Result == nil ||
				!validArtifactRef(task.Attempt.ID, task.Attempt.Result.Artifacts) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			if snapshot := task.Attempt.Result.Snapshot; filename == "change.diff" && snapshot != nil {
				patch, err := execution.ReadPatch(artifacts, task.Attempt.Result.Artifacts, snapshot.PatchSHA256)
				if err != nil {
					http.Error(w, "saved patch is unavailable or has changed", http.StatusConflict)
					return
				}
				http.ServeContent(w, r, filename, time.Time{}, bytes.NewReader(patch))
				return
			}
			f, err := artifacts.Open(path.Join(task.Attempt.Result.Artifacts, filename))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > execution.MaxEvidenceBytes {
				http.NotFound(w, r)
				return
			}
			http.ServeContent(w, r, filename, info.ModTime(), f)
		})
	}

	router.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Goal string `json:"goal"`
		}
		if !decode(w, r, &request) {
			return
		}
		goal := strings.TrimSpace(request.Goal)
		if goal == "" || utf8.RuneCountInString(goal) > 4096 || strings.ContainsRune(goal, 0) {
			http.Error(w, "goal must contain 1–4096 characters and no NUL", http.StatusBadRequest)
			return
		}
		task, err := tasks.Create(r.Context(), goal)
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, task)
	})

	router.HandleFunc("GET /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := readID(w, r)
		if !ok {
			return
		}
		task, err := tasks.Get(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	})

	router.HandleFunc("POST /tasks/claim", func(w http.ResponseWriter, r *http.Request) {
		if !decode(w, r, &struct{}{}) {
			return
		}
		task, err := tasks.Claim(r.Context())
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	})

	router.HandleFunc("POST /attempts/{id}/renew", func(w http.ResponseWriter, r *http.Request) {
		id, ok := readID(w, r)
		if !ok || !decode(w, r, &struct{}{}) {
			return
		}
		if err := tasks.Renew(r.Context(), id); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	router.HandleFunc("PUT /attempts/{id}/result", func(w http.ResponseWriter, r *http.Request) {
		id, ok := readID(w, r)
		if !ok {
			return
		}
		var request struct {
			ExitCode  *int            `json:"exit_code"`
			Output    *string         `json:"output"`
			Blocked   bool            `json:"blocked"`
			Artifacts string          `json:"artifacts"`
			Snapshot  *store.Snapshot `json:"snapshot"`
		}
		if !decode(w, r, &request) {
			return
		}
		if request.ExitCode == nil || *request.ExitCode < 0 || *request.ExitCode > 255 ||
			request.Output == nil || strings.ContainsRune(*request.Output, 0) ||
			(request.Artifacts != "" && !validArtifactRef(id, request.Artifacts)) ||
			(request.Blocked && (*request.ExitCode == 0 || request.Artifacts != "")) ||
			(request.Snapshot != nil && (request.Artifacts == "" || !execution.ValidSnapshot(*request.Snapshot))) {
			http.Error(w, "invalid result", http.StatusBadRequest)
			return
		}
		err := tasks.Complete(r.Context(), id, store.Result{
			ExitCode: *request.ExitCode, Output: *request.Output, Blocked: request.Blocked, Artifacts: request.Artifacts, Snapshot: request.Snapshot,
		})
		if err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return router
}

func validArtifactRef(id int64, reference string) bool {
	prefix := "attempt-" + strconv.FormatInt(id, 10) + "-"
	suffix, matches := strings.CutPrefix(reference, prefix)
	return len(reference) <= 64 && matches && artifactSuffix.MatchString(suffix)
}

func readID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Print("could not send JSON response")
	}
}

func storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "record not found", http.StatusNotFound)
	case errors.Is(err, store.ErrConflictingResult), errors.Is(err, store.ErrInactiveLease),
		errors.Is(err, store.ErrReviewBusy), errors.Is(err, store.ErrReviewApproved), errors.Is(err, store.ErrStaleCheck):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		log.Printf("store request failed (%T)", err)
		http.Error(w, "store request failed", http.StatusInternalServerError)
	}
}
