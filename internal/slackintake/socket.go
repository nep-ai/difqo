package slackintake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/nep-ai/difqo/internal/store"
)

const openSocketURL = "https://slack.com/api/apps.connections.open"

var (
	errSocketUnavailable = errors.New("Slack Socket Mode connection unavailable")
	errSocketDisabled    = errors.New("Slack Socket Mode is disabled for this app")
	errSocketAuth        = errors.New("Slack rejected the Socket Mode credentials or configuration")
	errSocketApp         = errors.New("Slack Socket Mode connection belongs to a different app")
	errSocketURL         = errors.New("Slack returned an invalid Socket Mode URL")
)

type envelope struct {
	Type           string          `json:"type"`
	ID             string          `json:"envelope_id"`
	Payload        json.RawMessage `json:"payload"`
	Reason         string          `json:"reason"`
	ConnectionInfo struct {
		AppID string `json:"app_id"`
	} `json:"connection_info"`
}

type slackCallback struct {
	Type          string `json:"type"`
	EventID       string `json:"event_id"`
	AppID         string `json:"api_app_id"`
	WorkspaceID   string `json:"team_id"`
	ExternalShare bool   `json:"is_ext_shared_channel"`
	Event         struct {
		Type        string `json:"type"`
		Channel     string `json:"channel"`
		ChannelType string `json:"channel_type"`
		User        string `json:"user"`
		UserTeam    string `json:"user_team"`
		Team        string `json:"team"`
		BotID       string `json:"bot_id"`
		AppID       string `json:"app_id"`
		Subtype     string `json:"subtype"`
		Text        string `json:"text"`
	} `json:"event"`
}

func socketGoal(binding store.SlackBinding, payload json.RawMessage) (eventID, goal string) {
	var callback slackCallback
	if json.Unmarshal(payload, &callback) != nil || callback.Type != "event_callback" || callback.ExternalShare {
		return "", ""
	}
	message := callback.Event
	if (message.Team != "" && message.Team != callback.WorkspaceID) ||
		(message.UserTeam != "" && message.UserTeam != callback.WorkspaceID) {
		return "", ""
	}
	bot := message.BotID != "" || message.AppID != ""
	value, err := authorizedGoal(binding, event{
		SlackBinding: store.SlackBinding{AppID: callback.AppID, WorkspaceID: callback.WorkspaceID,
			OperatorID: message.User, ChannelID: message.Channel},
		EventID: callback.EventID, Type: message.Type, ChannelType: message.ChannelType,
		Bot: &bot, Subtype: message.Subtype, Text: message.Text,
	})
	if err != nil {
		return "", ""
	}
	return callback.EventID, value
}

func RunSocketMode(ctx context.Context, tasks store.Tasks, binding store.SlackBinding, token string) error {
	if err := validateBinding(binding); err != nil {
		return err
	}
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("provide a Slack app-level token in DIFQO_SLACK_APP_TOKEN")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	generation, err := tasks.CheckSlackBinding(checkCtx, binding)
	cancel()
	if err != nil {
		if errors.Is(err, store.ErrSlackBinding) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not check the local Slack binding; verify PostgreSQL and migrations")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return runSocketMode(ctx, client, binding, token,
		func(ctx context.Context, binding store.SlackBinding, eventID, goal string) (bool, error) {
			return tasks.ImportPrivateSlack(ctx, binding, eventID, goal, generation)
		}, 2*time.Second)
}

func runSocketMode(ctx context.Context, client *http.Client, binding store.SlackBinding, token string,
	importGoal func(context.Context, store.SlackBinding, string, string) (bool, error), retryStart time.Duration) error {
	backoff := retryStart
	for ctx.Err() == nil {
		socketURL, retryAfter, err := openSocket(ctx, client, token)
		if err == nil {
			start := time.Now()
			err = socketSession(ctx, client, socketURL, binding, importGoal)
			if time.Since(start) > time.Minute {
				backoff = retryStart
			}
		}
		if ctx.Err() != nil {
			break
		}
		switch {
		case errors.Is(err, errSocketAuth), errors.Is(err, errSocketURL), errors.Is(err, errSocketDisabled),
			errors.Is(err, errSocketApp), errors.Is(err, store.ErrSlackBinding):
			return err
		}
		log.Print("Slack Socket Mode disconnected; reconnecting")
		timer := time.NewTimer(max(backoff, retryAfter))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(2*backoff, 30*time.Second)
	}
	return ctx.Err()
}

func openSocket(ctx context.Context, client *http.Client, token string) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openSocketURL, nil)
	if err != nil {
		return "", 0, errSocketUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, errSocketUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		seconds, _ := strconv.ParseUint(resp.Header.Get("Retry-After"), 10, 32)
		if seconds > 0 {
			return "", time.Duration(seconds) * time.Second, errSocketUnavailable
		}
		return "", 0, errSocketUnavailable
	}
	if resp.StatusCode >= 500 {
		return "", 0, errSocketUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, errSocketAuth
	}
	var body struct {
		OK    bool   `json:"ok"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&body); err != nil {
		return "", 0, errSocketUnavailable
	}
	if !body.OK {
		switch body.Error {
		case "internal_error", "fatal_error", "service_unavailable", "request_timeout":
			return "", 0, errSocketUnavailable
		default:
			return "", 0, errSocketAuth
		}
	}
	parsed, err := url.Parse(body.URL)
	if err != nil || parsed.Scheme != "wss" || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".slack.com") ||
		parsed.User != nil || (parsed.Port() != "" && parsed.Port() != "443") || parsed.Fragment != "" {
		return "", 0, errSocketURL
	}
	return body.URL, 0, nil
}

func socketSession(ctx context.Context, client *http.Client, socketURL string, binding store.SlackBinding,
	importGoal func(context.Context, store.SlackBinding, string, string) (bool, error)) error {
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, _, err := websocket.Dial(handshake, socketURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		cancel()
		return errSocketUnavailable
	}
	defer conn.CloseNow()
	conn.SetReadLimit(256 << 10)
	var hello envelope
	err = wsjson.Read(handshake, conn, &hello)
	cancel()
	if err != nil || hello.Type != "hello" {
		return errSocketUnavailable
	}
	if hello.ConnectionInfo.AppID != binding.AppID {
		return errSocketApp
	}
	log.Print("Slack Socket Mode connected")

	session, stop := context.WithCancel(ctx)
	defer stop()
	go keepSocketAlive(session, conn, stop)
	for {
		var incoming envelope
		if err := wsjson.Read(session, conn, &incoming); err != nil {
			return errSocketUnavailable
		}
		if incoming.Type == "disconnect" {
			if incoming.Reason == "link_disabled" {
				return errSocketDisabled
			}
			return errSocketUnavailable
		}
		if incoming.ID == "" {
			continue
		}
		if incoming.Type == "events_api" {
			eventID, goal := socketGoal(binding, incoming.Payload)
			if eventID != "" {
				writeCtx, done := context.WithTimeout(session, 2*time.Second)
				_, err := importGoal(writeCtx, binding, eventID, goal)
				done()
				if err != nil && !errors.Is(err, store.ErrSlackReplay) {
					// Leave this envelope unacknowledged so Slack can retry.
					if errors.Is(err, store.ErrSlackBinding) {
						return err
					}
					return errSocketUnavailable
				}
			}
		}
		// ACK ignored events and committed goals. The envelope ID is for
		// delivery; the Events API event ID is the durable replay key.
		ackCtx, done := context.WithTimeout(session, 750*time.Millisecond)
		err := wsjson.Write(ackCtx, conn, struct {
			ID string `json:"envelope_id"`
		}{ID: incoming.ID})
		done()
		if err != nil {
			return errSocketUnavailable
		}
	}
}

func keepSocketAlive(ctx context.Context, conn *websocket.Conn, stop context.CancelFunc) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, done := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			done()
			if err != nil {
				stop()
				return
			}
		}
	}
}
