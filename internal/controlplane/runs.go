package controlplane

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/nep-ai/difqo/internal/store"
)

func registerRuns(router *http.ServeMux, tasks store.Tasks, specialists store.Specialists) {
	router.HandleFunc("GET /specialist-projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, append([]store.SpecialistProject{}, specialists.ProjectCatalog()...))
	})
	router.HandleFunc("POST /specialist-runs", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			RequestID    string `json:"request_id"`
			Goal         string `json:"goal"`
			SpecialistID int64  `json:"specialist_id"`
			Revision     int    `json:"revision"`
			Project      string `json:"project"`
			Binding      string `json:"binding"`
		}
		if !decode(w, r, &input) {
			return
		}
		goal := strings.TrimSpace(input.Goal)
		if !proposalID.MatchString(input.RequestID) || !specialistText(goal, 4096) ||
			input.SpecialistID < 1 || input.Revision < 1 || input.Project == "" || input.Binding == "" {
			http.Error(w, "enter a goal and select one specialist revision and project", http.StatusBadRequest)
			return
		}
		project, found := specialists.Project(input.Project)
		if !found || project.Binding != input.Binding {
			http.Error(w, "the selected project binding changed or is unavailable", http.StatusForbidden)
			return
		}
		task, err := specialists.Queue(r.Context(), input.RequestID, goal, input.SpecialistID, input.Revision, project)
		switch {
		case errors.Is(err, store.ErrRunUnauthorized):
			http.Error(w, err.Error(), http.StatusForbidden)
		case errors.Is(err, store.ErrRunConflict):
			http.Error(w, err.Error(), http.StatusConflict)
		case err != nil:
			storeError(w, err)
		default:
			writeJSON(w, http.StatusCreated, task)
		}
	})
	router.HandleFunc("POST /specialist-runs/claim", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Project string `json:"project"`
			Binding string `json:"binding"`
		}
		if !decode(w, r, &input) {
			return
		}
		project, found := specialists.Project(input.Project)
		if !found || project.Binding != input.Binding {
			http.Error(w, "worker checkout binding does not match a registered project", http.StatusForbidden)
			return
		}
		task, err := tasks.ClaimProject(r.Context(), project)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			w.WriteHeader(http.StatusNoContent)
		case err != nil:
			storeError(w, err)
		default:
			writeJSON(w, http.StatusOK, task)
		}
	})
	router.HandleFunc("GET /specialist-runs/{id}/attempts", func(w http.ResponseWriter, r *http.Request) {
		id, ok := readID(w, r)
		if !ok {
			return
		}
		history, err := specialists.History(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, history)
	})
}
