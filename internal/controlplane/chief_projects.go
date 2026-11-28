package controlplane

import (
	"errors"
	"net/http"

	"github.com/nep-ai/difqo/internal/store"
)

type chiefProjectEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func registerChiefProjects(router *http.ServeMux, tasks store.Tasks, projects func() []store.SpecialistProject, add func(string, string, string) (store.SpecialistProject, error)) {
	router.HandleFunc("GET /chief/projects", func(w http.ResponseWriter, _ *http.Request) {
		list := []chiefProjectEntry{}
		for _, project := range projects() {
			if project.ExecutionBinding != "" {
				list = append(list, chiefProjectEntry{ID: project.ID, Name: project.Name})
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, struct {
			Projects []chiefProjectEntry `json:"projects"`
			CanAdd   bool                `json:"can_add"`
		}{list, add != nil})
	})
	router.HandleFunc("POST /chief/projects", func(w http.ResponseWriter, r *http.Request) {
		if add == nil {
			http.Error(w, "Project setup is unavailable for this service.", http.StatusMethodNotAllowed)
			return
		}
		var input struct {
			Name     string `json:"name"`
			Checkout string `json:"checkout"`
			Check    string `json:"check"`
		}
		if !decode(w, r, &input) {
			return
		}
		project, err := add(input.Name, input.Checkout, input.Check)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, chiefProjectEntry{ID: project.ID, Name: project.Name})
	})
	router.HandleFunc("POST /chief/conversation/{id}/project", func(w http.ResponseWriter, r *http.Request) {
		id, ok := readID(w, r)
		var input struct {
			ProjectID string `json:"project_id"`
		}
		if !ok || !decode(w, r, &input) {
			return
		}
		for _, project := range projects() {
			if project.ID == input.ProjectID && project.ExecutionBinding != "" {
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
				return
			}
		}
		http.Error(w, "Select an available project with a configured check.", http.StatusBadRequest)
	})
}
