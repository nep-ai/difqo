package controlplane

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/execution"
	"github.com/nep-ai/difqo/internal/store"
)

var chatRequestID = regexp.MustCompile(`^[a-f0-9-]{36}$`)

func registerChief(router *http.ServeMux, tasks store.Tasks, projects func() []store.SpecialistProject, add func(string, string, string) (store.SpecialistProject, error), talk func(context.Context, []store.ChatTurn, store.ChatTurn) (store.ChiefAnswer, error)) {
	registerChiefProjects(router, tasks, projects, add)
	router.HandleFunc("GET /chief/conversation", func(w http.ResponseWriter, r *http.Request) {
		turns, err := tasks.ChiefTurns(r.Context())
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, turns)
	})
	router.HandleFunc("POST /chief/conversation", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			RequestID string             `json:"request_id"`
			Text      string             `json:"text"`
			Files     []store.Attachment `json:"attachments,omitempty"`
		}
		if !decodeLimit(w, r, &input, maxMessageBody) {
			return
		}
		input.Text = strings.TrimSpace(input.Text)
		if !chatRequestID.MatchString(input.RequestID) || (input.Text == "" && len(input.Files) == 0) ||
			utf8.RuneCountInString(input.Text) > 4096 || strings.ContainsRune(input.Text, 0) {
			http.Error(w, "Enter a message of up to 4,096 characters or add an attachment.", http.StatusBadRequest)
			return
		}
		if err := validateAttachments(input.Files); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		message := store.ChatTurn{Text: input.Text, Attachments: input.Files}
		saved, started, err := tasks.StartChiefTurn(r.Context(), input.RequestID, message)
		if errors.Is(err, store.ErrChatBusy) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			storeError(w, err)
			return
		}
		if started {
			go finishChief(tasks, talk, saved.ID, message)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, saved)
	})
}

func finishChief(tasks store.Tasks, talk func(context.Context, []store.ChatTurn, store.ChatTurn) (store.ChiefAnswer, error), id int64, message store.ChatTurn) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	answer, err := runChief(ctx, tasks, talk, id, message)
	problem := ""
	switch {
	case errors.Is(err, execution.ErrAuthentication):
		problem = "Sign in with ChatGPT in Connections, then send your message again."
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		problem = "The conversation timed out. Try sending your message again."
	case err != nil:
		problem = "Chief of Staff couldn’t respond. Check Docker and ChatGPT in Connections, then try again."
	}
	finish, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := tasks.FinishChiefTurn(finish, id, answer.Reply, problem, answer.Goal); err != nil {
		log.Printf("could not record Chief of Staff turn %d (%T)", id, err)
	}
}

func runChief(ctx context.Context, tasks store.Tasks, talk func(context.Context, []store.ChatTurn, store.ChatTurn) (store.ChiefAnswer, error), id int64, message store.ChatTurn) (store.ChiefAnswer, error) {
	turns, err := tasks.ChiefTurns(ctx)
	if err != nil {
		return store.ChiefAnswer{}, err
	}
	history := []store.ChatTurn{}
	const budget = 32 << 10
	var used int
	for _, turn := range turns {
		if turn.ID >= id || turn.State != "completed" {
			continue
		}
		used += len(turn.Text) + len(turn.Reply)
		if used > budget || len(history) == 8 {
			break
		}
		history = append(history, turn)
	}
	if len(history) > 0 && len(message.Attachments) == 0 &&
		history[0].Text == "" && len(history[0].Attachments) > 0 {
		message.Attachments, err = tasks.ChiefAttachments(ctx, history[0].ID)
		if err != nil {
			return store.ChiefAnswer{}, err
		}
	}
	slices.Reverse(history)
	answer, err := talk(ctx, history, message)
	if err != nil {
		return store.ChiefAnswer{}, err
	}
	answer.Reply = strings.TrimSpace(strings.ReplaceAll(answer.Reply, "\x00", ""))
	if answer.Reply == "" {
		return store.ChiefAnswer{}, errors.New("Chief of Staff did not reply")
	}
	answer.Goal = strings.TrimSpace(answer.Goal)
	if strings.ContainsRune(answer.Goal, 0) || utf8.RuneCountInString(answer.Goal) > 4096 {
		answer.Goal = ""
	}
	return answer, nil
}
