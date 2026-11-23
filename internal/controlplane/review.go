package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/execution"
	"github.com/nep-ai/difqo/internal/store"
)

func registerDecisions(router *http.ServeMux, tasks store.Tasks, specialists store.Specialists) {
	router.HandleFunc("GET /project-decision", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		project, ok := decisionProject(w, r, specialists)
		if !ok {
			return
		}
		decision, err := tasks.LatestDecision(r.Context(), project)
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, decision)
	})
	router.HandleFunc("POST /project-decision", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Body string `json:"body"`
		}
		if !decode(w, r, &input) {
			return
		}
		body := strings.TrimSpace(input.Body)
		if body == "" || utf8.RuneCountInString(body) > 4096 || strings.ContainsRune(body, 0) {
			http.Error(w, "decision must contain 1–4096 characters and no NUL", http.StatusBadRequest)
			return
		}
		project, ok := decisionProject(w, r, specialists)
		if !ok {
			return
		}
		decision, err := tasks.SaveProjectDecision(r.Context(), body, project)
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, decision)
	})
}

func decisionProject(w http.ResponseWriter, r *http.Request, specialists store.Specialists) (store.SpecialistProject, bool) {
	id := r.URL.Query().Get("project")
	if id == "" {
		return store.SpecialistProject{}, true
	}
	project, found := specialists.Project(id)
	if !found || project.Binding != r.URL.Query().Get("binding") {
		http.Error(w, "project binding changed or is unavailable; reload the project catalog", http.StatusConflict)
		return store.SpecialistProject{}, false
	}
	return project, true
}

func registerReview(router *http.ServeMux, tasks store.Tasks, artifacts *os.Root, runtimeChoice string) {
	router.HandleFunc("GET /tasks/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		attempt, ok := reviewAttempt(w, r, tasks)
		if !ok {
			return
		}
		review, err := tasks.Review(r.Context(), attempt.ID)
		if err != nil {
			storeError(w, err)
			return
		}
		_, patchErr := execution.ReadPatch(artifacts, attempt.Result.Artifacts, attempt.Result.Snapshot.PatchSHA256)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, struct {
			store.Review
			PatchAvailable bool `json:"patch_available"`
		}{review, patchErr == nil})
	})

	router.HandleFunc("POST /tasks/{id}/review/{action}", func(w http.ResponseWriter, r *http.Request) {
		action := r.PathValue("action")
		if action != "rerun" && action != "approve" {
			http.NotFound(w, r)
			return
		}
		var input struct {
			PatchSHA256 string `json:"patch_sha256"`
			CheckID     int64  `json:"check_id"`
		}
		if !decode(w, r, &input) {
			return
		}
		attempt, ok := reviewAttempt(w, r, tasks)
		if !ok {
			return
		}
		result := attempt.Result
		if input.PatchSHA256 != result.Snapshot.PatchSHA256 {
			http.Error(w, "reviewed patch does not match this run", http.StatusConflict)
			return
		}
		patch, err := execution.ReadPatch(artifacts, result.Artifacts, input.PatchSHA256)
		if err != nil {
			http.Error(w, "saved patch is unavailable or has changed", http.StatusConflict)
			return
		}
		if action == "approve" {
			if err := tasks.Approve(r.Context(), attempt.ID, input.CheckID); err != nil {
				storeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// The bounded check can outlast the server's normal write deadline.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second)); err != nil {
			http.Error(w, "could not reserve time for the check", http.StatusInternalServerError)
			return
		}
		checkID, err := tasks.StartCheck(r.Context(), attempt.ID)
		if err != nil {
			storeError(w, err)
			return
		}
		code, output := execution.Rerun(r.Context(), artifacts, result.Artifacts, *result.Snapshot, patch, runtimeChoice)
		// Persist a terminal failure even if the browser canceled its request.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer cancel()
		if err := tasks.FinishCheck(ctx, checkID, code, output); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func reviewAttempt(w http.ResponseWriter, r *http.Request, tasks store.Tasks) (*store.Attempt, bool) {
	id, ok := readID(w, r)
	if !ok {
		return nil, false
	}
	task, err := tasks.Get(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return nil, false
	}
	attempt := task.Attempt
	if attempt == nil || attempt.Result == nil || attempt.Result.Snapshot == nil ||
		!validArtifactRef(attempt.ID, attempt.Result.Artifacts) {
		http.NotFound(w, r)
		return nil, false
	}
	return attempt, true
}
