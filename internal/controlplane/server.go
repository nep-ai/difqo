package controlplane

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
)

func Handler(tasks store.Tasks) http.Handler {
	router := http.NewServeMux()

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
			ExitCode *int    `json:"exit_code"`
			Output   *string `json:"output"`
		}
		if !decode(w, r, &request) {
			return
		}
		if request.ExitCode == nil || *request.ExitCode < 0 || *request.ExitCode > 255 ||
			request.Output == nil || strings.ContainsRune(*request.Output, 0) {
			http.Error(w, "invalid result", http.StatusBadRequest)
			return
		}
		err := tasks.Complete(r.Context(), id, store.Result{ExitCode: *request.ExitCode, Output: *request.Output})
		if err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return router
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
	case errors.Is(err, store.ErrConflictingResult), errors.Is(err, store.ErrInactiveLease):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		log.Printf("store request failed (%T)", err)
		http.Error(w, "store request failed", http.StatusInternalServerError)
	}
}
