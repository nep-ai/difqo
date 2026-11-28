package controlplane

import (
	"errors"
	"net/http"

	"github.com/nep-ai/difqo/internal/store"
)

func registerChiefProjects(router *http.ServeMux, tasks store.Tasks, projects []store.SpecialistProject) {
	eligible := make(map[string]store.SpecialistProject)
	type entry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	list := []entry{}
	for _, project := range projects {
		if project.ExecutionBinding != "" {
			eligible[project.ID] = project
			list = append(list, entry{ID: project.ID, Name: project.Name})
		}
	}
	router.HandleFunc("GET /chief/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, list)
	})
	router.HandleFunc("POST /chief/conversation/{id}/project", func(w http.ResponseWriter, r *http.Request) {
		id, ok := readID(w, r)
		var input struct {
			ProjectID string `json:"project_id"`
		}
		if !ok || !decode(w, r, &input) {
			return
		}
		project, found := eligible[input.ProjectID]
		if !found {
			http.Error(w, "Select an available project with a configured check.", http.StatusBadRequest)
			return
		}
		task, err := tasks.ConfirmChiefProject(r.Context(), id, project)
		if errors.Is(err, store.ErrChiefProjectConflict) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, task)
	})
}
