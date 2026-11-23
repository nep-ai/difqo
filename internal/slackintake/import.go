package slackintake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
)

const maxEventSize = 64 << 10

var idPattern = regexp.MustCompile(`^[A-Z0-9]{2,64}$`)

type event struct {
	store.SlackBinding
	EventID     string `json:"event_id"`
	Type        string `json:"type"`
	ChannelType string `json:"channel_type"`
	Bot         *bool  `json:"bot"`
	Subtype     string `json:"subtype"`
	Text        string `json:"text"`
}

func validateBinding(binding store.SlackBinding) error {
	for _, id := range []string{binding.AppID, binding.WorkspaceID, binding.OperatorID, binding.ChannelID} {
		if !idPattern.MatchString(id) {
			return errors.New("provide valid app, workspace, operator, and DM IDs")
		}
	}
	return nil
}

// Import accepts a normalized event from a trusted local input. Network
// adapters must authenticate delivery before constructing this event.
func Import(ctx context.Context, tasks store.Tasks, binding store.SlackBinding, input io.Reader) (bool, error) {
	if err := validateBinding(binding); err != nil {
		return false, err
	}
	limited := &io.LimitedReader{R: input, N: maxEventSize + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var incoming event
	if err := decoder.Decode(&incoming); err != nil || decoder.Decode(new(any)) != io.EOF || limited.N == 0 {
		return false, errors.New("expected one bounded normalized Slack event")
	}
	if incoming.SlackBinding != binding || incoming.Type != "message" || incoming.ChannelType != "im" ||
		incoming.Bot == nil || *incoming.Bot || incoming.Subtype != "" {
		return false, errors.New("event is outside the linked operator's private DM")
	}
	if !idPattern.MatchString(incoming.EventID) {
		return false, errors.New("invalid Slack event ID")
	}
	goal, ok := strings.CutPrefix(strings.TrimSpace(incoming.Text), "goal ")
	goal = strings.TrimSpace(goal)
	if !ok || goal == "" || strings.ContainsRune(goal, 0) || utf8.RuneCountInString(goal) > 4096 {
		return false, errors.New("use goal followed by a task of up to 4096 characters")
	}
	return tasks.ImportSlack(ctx, binding, incoming.EventID, goal)
}
