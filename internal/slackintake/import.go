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
var eventIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{2,64}$`)

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
	incoming, err := readEvent(ctx, input)
	if err != nil {
		return false, err
	}
	goal, err := authorizedGoal(binding, incoming)
	if err != nil {
		return false, err
	}
	return tasks.ImportSlack(ctx, binding, incoming.EventID, goal)
}

func authorizedGoal(binding store.SlackBinding, incoming event) (string, error) {
	if incoming.SlackBinding != binding {
		return "", errors.New("event is outside the linked operator's private DM")
	}
	return incoming.goal(true)
}

func (incoming event) goal(requirePrefix bool) (string, error) {
	if incoming.Type != "message" || incoming.ChannelType != "im" ||
		incoming.Bot == nil || *incoming.Bot || incoming.Subtype != "" {
		return "", errors.New("event is outside the linked operator's private DM")
	}
	if !eventIDPattern.MatchString(incoming.EventID) {
		return "", errors.New("invalid Slack event ID")
	}
	goal, ok := strings.CutPrefix(strings.TrimSpace(incoming.Text), "goal ")
	goal = strings.TrimSpace(goal)
	if requirePrefix && !ok {
		return "", errors.New("use goal followed by a task")
	}
	if !validGoal(goal) {
		return "", errors.New("enter a task of 1 to 4096 characters without NUL")
	}
	return goal, nil
}

func validGoal(goal string) bool {
	return goal != "" && goal == strings.TrimSpace(goal) && !strings.ContainsRune(goal, 0) && utf8.RuneCountInString(goal) <= 4096
}

func readEvent(ctx context.Context, input io.Reader) (event, error) {
	if err := ctx.Err(); err != nil {
		return event{}, err
	}
	type result struct {
		event event
		err   error
	}
	done := make(chan result, 1)
	// Keep the blocking read separate from task creation. The CLI owns stdin
	// for this one import, so exiting also ends any read still waiting for EOF.
	go func() {
		limited := &io.LimitedReader{R: input, N: maxEventSize + 1}
		decoder := json.NewDecoder(limited)
		decoder.DisallowUnknownFields()
		var incoming event
		if err := decoder.Decode(&incoming); err != nil || decoder.Decode(new(any)) != io.EOF || limited.N == 0 {
			done <- result{err: errors.New("expected one bounded normalized Slack event")}
			return
		}
		done <- result{event: incoming}
	}()
	select {
	case <-ctx.Done():
		return event{}, ctx.Err()
	case decoded := <-done:
		if err := ctx.Err(); err != nil {
			return event{}, err
		}
		return decoded.event, decoded.err
	}
}
