package execution

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

func runCodex(stdin io.Writer, stdout io.Reader, goal string) error {
	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 64<<10), 8<<20)
	s := appServer{input: reader, output: json.NewEncoder(stdin)}
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
		"developerInstructions": "Make only the requested change in the workspace. Leave review and application of the patch to the operator.",
	}, &started); err != nil {
		return err
	}
	if started.Thread.ID == "" {
		return errors.New("Codex App Server did not return a thread ID")
	}

	var turn struct {
		Turn struct{ ID string } `json:"turn"`
	}
	if err := s.call(4, "turn/start", map[string]any{
		"threadId": started.Thread.ID,
		"input":    []map[string]string{{"type": "text", "text": goal}},
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
