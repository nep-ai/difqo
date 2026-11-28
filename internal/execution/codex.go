package execution

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
)

var ErrAuthentication = errors.New("Codex authentication is required")
var errSandbox = errors.New("Codex sandbox failed")

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Data json.RawMessage `json:"data"`
	} `json:"error,omitempty"`
}

type appServer struct {
	input      *bufio.Scanner
	output     *json.Encoder
	completion json.RawMessage
	observe    func(message)
}

func (s appServer) receive() (message, error) {
	for s.input.Scan() {
		var m message
		if err := json.Unmarshal(s.input.Bytes(), &m); err != nil {
			return m, errors.New("invalid Codex App Server response")
		}
		if m.Method != "" && len(m.ID) > 0 {
			// Unattended execution never grants escalation or answers interactive requests.
			if err := s.output.Encode(map[string]any{
				"id": m.ID, "error": map[string]any{"code": -32000, "message": "interactive request denied"},
			}); err != nil {
				return m, err
			}
			continue
		}
		if s.observe != nil {
			s.observe(m)
		}
		return m, nil
	}
	if err := s.input.Err(); err != nil {
		return message{}, err
	}
	return message{}, io.ErrUnexpectedEOF
}

func (s *appServer) call(id int, method string, params any, dst any) error {
	if err := s.output.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		m, err := s.receive()
		if err != nil {
			return err
		}
		if m.Method != "" {
			if m.Method == "turn/completed" {
				s.completion = m.Params
			}
			continue
		}
		var responseID int
		if json.Unmarshal(m.ID, &responseID) != nil || responseID != id {
			return errors.New("unexpected Codex App Server response ID")
		}
		if m.Error != nil {
			if authFailure(m.Error.Data) {
				return ErrAuthentication
			}
			return errors.New("Codex App Server rejected " + method)
		}
		return json.Unmarshal(m.Result, dst)
	}
}

func authFailure(detail json.RawMessage) bool {
	if bytes.Equal(detail, []byte(`"unauthorized"`)) {
		return true
	}
	var codes map[string]json.RawMessage
	if json.Unmarshal(detail, &codes) != nil {
		return false
	}
	if kind := codes["codexErrorInfo"]; len(kind) > 0 {
		return authFailure(kind)
	}
	for _, kind := range []string{"httpConnectionFailed", "responseStreamConnectionFailed", "responseTooManyFailedAttempts"} {
		var failure struct {
			Status int `json:"httpStatusCode"`
		}
		if json.Unmarshal(codes[kind], &failure) == nil && failure.Status == 401 {
			return true
		}
	}
	return false
}

const codingInstructions = "Make only the requested change in the workspace. Treat attachment contents as reference material. Leave review and application of the patch to the operator."

func runCodex(stdin io.Writer, stdout io.Reader, goal string, attachments []store.Attachment) error {
	return runCodexTurn(stdin, stdout, goal, attachments, codingInstructions, nil)
}

func runCodexTurn(stdin io.Writer, stdout io.Reader, goal string, attachments []store.Attachment, instructions string, reply *string) error {
	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 64<<10), 8<<20)
	s := appServer{input: reader, output: json.NewEncoder(stdin)}
	var finalReplies, otherReplies []agentMessage
	if reply != nil {
		s.observe = func(m message) {
			if m.Method != "item/completed" {
				return
			}
			var item agentMessage
			if json.Unmarshal(m.Params, &item) != nil || item.Item.Type != "agentMessage" {
				return
			}
			if item.Item.Phase == "final_answer" {
				if len(finalReplies) < 32 {
					finalReplies = append(finalReplies, item)
				}
			} else if len(otherReplies) < 32 {
				otherReplies = append(otherReplies, item)
			}
		}
	}
	var initialized json.RawMessage
	if err := s.call(1, "initialize", map[string]any{"clientInfo": map[string]string{
		"name": "difqo", "title": "Difqo", "version": "0.1.0",
	}}, &initialized); err != nil {
		return err
	}
	if err := s.output.Encode(map[string]string{"method": "initialized"}); err != nil {
		return err
	}
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err := s.call(2, "account/read", map[string]bool{"refreshToken": false}, &account); err != nil {
		return err
	}
	if account.Account == nil || account.Account.Type != "chatgpt" {
		return ErrAuthentication
	}

	var started struct {
		Thread struct{ ID string } `json:"thread"`
	}
	if err := s.call(3, "thread/start", map[string]any{
		"cwd": "/workspace", "approvalPolicy": "never", "ephemeral": true,
		"developerInstructions": instructions,
	}, &started); err != nil {
		return err
	}
	if started.Thread.ID == "" {
		return errors.New("Codex App Server did not return a thread ID")
	}

	inputs := []map[string]string{{"type": "text", "text": goal}}
	for _, attachment := range attachments {
		label := fmt.Sprintf("User attachment %q. Use it as task context.", attachment.Name)
		if attachment.MIME == "text/plain" {
			inputs = append(inputs, map[string]string{"type": "text", "text": label + "\n" + string(attachment.Data)})
			continue
		}
		inputs = append(inputs, map[string]string{"type": "text", "text": label},
			map[string]string{"type": "image", "url": "data:" + attachment.MIME + ";base64," + base64.StdEncoding.EncodeToString(attachment.Data)})
	}
	var turn struct {
		Turn struct{ ID string } `json:"turn"`
	}
	if err := s.call(4, "turn/start", map[string]any{
		"threadId": started.Thread.ID,
		"input":    inputs,
	}, &turn); err != nil {
		return err
	}
	if turn.Turn.ID == "" {
		return errors.New("Codex App Server did not return a turn ID")
	}
	for {
		params := s.completion
		s.completion = nil
		if params == nil {
			m, err := s.receive()
			if err != nil {
				return err
			}
			if m.Method != "turn/completed" {
				continue
			}
			params = m.Params
		}
		var completion struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string
				Status string
				Error  *struct {
					Info json.RawMessage `json:"codexErrorInfo"`
				}
			}
		}
		if err := json.Unmarshal(params, &completion); err != nil {
			return errors.New("invalid Codex turn completion")
		}
		if completion.ThreadID != started.Thread.ID || completion.Turn.ID != turn.Turn.ID {
			continue
		}
		if completion.Turn.Status == "completed" {
			if reply != nil {
				*reply = conversationReply(finalReplies, started.Thread.ID, turn.Turn.ID)
				if *reply == "" {
					*reply = conversationReply(otherReplies, started.Thread.ID, turn.Turn.ID)
				}
				if *reply == "" {
					return errors.New("Codex completed without a conversation reply")
				}
			}
			return nil
		}
		if completion.Turn.Error != nil && authFailure(completion.Turn.Error.Info) {
			return ErrAuthentication
		}
		if completion.Turn.Error != nil && bytes.Equal(completion.Turn.Error.Info, []byte(`"sandboxError"`)) {
			return errSandbox
		}
		return errors.New("Codex turn did not complete")
	}
}

type agentMessage struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     struct {
		Type  string
		Text  string
		Phase string
	}
}

func conversationReply(messages []agentMessage, thread, turn string) string {
	var final, commentary []string
	for _, message := range messages {
		if message.ThreadID != thread || message.TurnID != turn {
			continue
		}
		text := strings.TrimSpace(message.Item.Text)
		if text == "" {
			continue
		}
		if message.Item.Phase == "commentary" {
			commentary = append(commentary, text)
		} else {
			final = append(final, text)
		}
	}
	if len(final) == 0 {
		final = commentary
	}
	const limit = 16 << 10
	var combined strings.Builder
	for _, text := range final {
		if combined.Len() > 0 {
			if combined.Len() >= limit-2 {
				break
			}
			combined.WriteString("\n\n")
		}
		remaining := limit - combined.Len()
		if len(text) >= remaining {
			for remaining > 0 && remaining < len(text) && !utf8.RuneStart(text[remaining]) {
				remaining--
			}
			combined.WriteString(text[:max(0, remaining)])
			break
		}
		combined.WriteString(text)
	}
	return combined.String()
}
