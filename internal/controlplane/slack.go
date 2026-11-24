package controlplane

import (
	"errors"
	"net/http"

	"github.com/nep-ai/difqo/internal/slackintake"
	"github.com/nep-ai/difqo/internal/store"
)

func registerSlackConnection(router *http.ServeMux, connections *slackintake.Connections) {
	respond := func(w http.ResponseWriter, view slackintake.ConnectionView, err error) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		switch {
		case errors.Is(err, store.ErrSlackTransition):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, slackintake.ErrRelayUnavailable):
			writeJSON(w, http.StatusBadGateway, view)
		case err != nil:
			storeError(w, err)
		default:
			writeJSON(w, http.StatusOK, view)
		}
	}
	router.HandleFunc("GET /slack/connection", func(w http.ResponseWriter, r *http.Request) {
		view, err := connections.Status(r.Context())
		respond(w, view, err)
	})
	router.HandleFunc("POST /slack/connection", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action     string             `json:"action"`
			PairingID  string             `json:"pairing_id"`
			Generation int64              `json:"generation"`
			Binding    store.SlackBinding `json:"binding"`
		}
		if !decode(w, r, &in) {
			return
		}
		var view slackintake.ConnectionView
		var err error
		switch in.Action {
		case "begin":
			view, err = connections.Begin(r.Context())
		case "start_identity", "start_install", "approval_needed":
			view, err = connections.OAuth(r.Context(), in.PairingID, in.Action)
		case "refresh":
			view, err = connections.Refresh(r.Context(), in.PairingID)
		case "confirm":
			view, err = connections.Confirm(r.Context(), in.PairingID, in.Binding)
		case "disconnect":
			view, err = connections.Disconnect(r.Context(), in.Generation, in.PairingID)
		case "enable_private":
			view, err = connections.EnablePrivate(r.Context(), in.Generation)
		default:
			http.Error(w, "unknown Slack connection action", http.StatusBadRequest)
			return
		}
		respond(w, view, err)
	})
}
