package controlplane

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
)

var proposalID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func registerSpecialists(router *http.ServeMux, specialists store.Specialists) {
	router.HandleFunc("GET /specialists", func(w http.ResponseWriter, r *http.Request) {
		state, err := specialists.State(r.Context())
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, state)
	})
	router.HandleFunc("GET /specialists/{id}/eligibility", func(w http.ResponseWriter, r *http.Request) {
		id, ok := readID(w, r)
		if !ok {
			return
		}
		project := r.URL.Query().Get("project")
		if project == "" {
			http.Error(w, "select a project", http.StatusBadRequest)
			return
		}
		eligible, err := specialists.Eligible(r.Context(), id, project)
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]bool{"eligible": eligible})
	})
	router.HandleFunc("POST /specialist-proposals", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID           string               `json:"id"`
			SpecialistID int64                `json:"specialist_id"`
			BaseRevision int                  `json:"base_revision"`
			Action       string               `json:"action"`
			Name         string               `json:"name"`
			Purpose      string               `json:"purpose"`
			Output       string               `json:"output"`
			Report       store.SnapshotReport `json:"report"`
			Projects     []string             `json:"projects"`
		}
		if !decode(w, r, &input) {
			return
		}
		switch input.Action {
		case "create":
			if input.SpecialistID != 0 || input.BaseRevision != 0 {
				http.Error(w, "creation cannot target an existing specialist", http.StatusBadRequest)
				return
			}
		case "update", "disable":
			if input.SpecialistID < 1 || input.BaseRevision < 1 {
				http.Error(w, "select a specialist revision", http.StatusBadRequest)
				return
			}
		default:
			http.Error(w, "invalid specialist action", http.StatusBadRequest)
			return
		}
		if !proposalID.MatchString(input.ID) {
			http.Error(w, "invalid proposal id", http.StatusBadRequest)
			return
		}

		config := store.SpecialistConfig{
			Name: strings.TrimSpace(input.Name), Purpose: strings.TrimSpace(input.Purpose), Output: strings.TrimSpace(input.Output), Report: input.Report,
		}
		if !input.Report.Valid() {
			http.Error(w, "choose a supported snapshot report", http.StatusBadRequest)
			return
		}
		if input.Action != "disable" && (!specialistText(config.Name, 100) || !specialistText(config.Purpose, 4096) ||
			!specialistText(config.Output, 1024) || len(input.Projects) > 32) {
			http.Error(w, "enter a name, purpose and expected output, and select up to 32 projects", http.StatusBadRequest)
			return
		}
		p, err := specialists.Propose(r.Context(), store.SpecialistProposal{
			ID: input.ID, SpecialistID: input.SpecialistID, BaseRevision: input.BaseRevision, Action: input.Action, Config: config,
		}, input.Projects)
		switch {
		case errors.Is(err, store.ErrSpecialistProject):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "proposal could not be saved for this target or request id", http.StatusConflict)
		case err != nil:
			storeError(w, err)
		default:
			writeJSON(w, http.StatusCreated, p)
		}
	})
	router.HandleFunc("POST /specialist-proposals/{proposal}/decision", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("proposal")
		var input struct {
			Outcome string `json:"outcome"`
		}
		if !decode(w, r, &input) {
			return
		}
		if !proposalID.MatchString(id) || (input.Outcome != "approve" && input.Outcome != "decline" && input.Outcome != "revise") {
			http.Error(w, "invalid proposal decision", http.StatusBadRequest)
			return
		}
		p, err := specialists.Decide(r.Context(), id, input.Outcome)
		if errors.Is(err, store.ErrSpecialistStale) {
			http.Error(w, err.Error(), http.StatusConflict)
		} else if err != nil {
			storeError(w, err)
		} else {
			writeJSON(w, http.StatusOK, p)
		}
	})
}

func specialistText(value string, limit int) bool {
	return value != "" && utf8.RuneCountInString(value) <= limit && !strings.ContainsRune(value, '\x00')
}
