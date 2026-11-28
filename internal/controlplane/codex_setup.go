package controlplane

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nep-ai/difqo/internal/store"
)

const (
	deviceSignInURL = "https://auth.openai.com/codex/device"
	sampleGoal      = "In greeting.txt, replace the line before with the line after. Leave other files unchanged."
)

var (
	terminalColor = regexp.MustCompile("\\x1b\\[[0-9;]*m")
	deviceCode    = regexp.MustCompile(`^[A-Z0-9]{3,8}-[A-Z0-9]{3,8}$`)
)

type codexSetupView struct {
	State   string `json:"state"`
	URL     string `json:"url,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	TaskID  int64  `json:"task_id,omitempty"`
}

type codexSetup struct {
	mu   sync.Mutex
	view codexSetupView
}

func registerCodexSetup(router *http.ServeMux, helper string, tasks store.Tasks) {
	setup := &codexSetup{view: codexSetupView{State: "unknown"}}
	respond := func(w http.ResponseWriter, status int) {
		setup.mu.Lock()
		view := setup.view
		setup.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, status, view)
	}
	router.HandleFunc("GET /setup/codex", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, http.StatusOK)
	})
	router.HandleFunc("POST /setup/codex", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Action string `json:"action"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Action != "prepare" && input.Action != "check" {
			http.Error(w, "unknown ChatGPT setup action", http.StatusBadRequest)
			return
		}
		if !setup.start(helper, input.Action, 0) {
			http.Error(w, "Codex is already working. Try again after it finishes.", http.StatusConflict)
			return
		}
		respond(w, http.StatusAccepted)
	})
	router.HandleFunc("POST /setup/codex/sample/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !decode(w, r, &struct{}{}) {
			return
		}
		id, ok := readID(w, r)
		if !ok {
			return
		}
		task, err := tasks.Get(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		if task.Status != "queued" || task.Goal != sampleGoal || task.Run != nil {
			http.Error(w, "Only the queued sample goal can use this worker.", http.StatusConflict)
			return
		}
		if !setup.start(helper, "run", id) {
			http.Error(w, "Codex is already working. Try again after it finishes.", http.StatusConflict)
			return
		}
		respond(w, http.StatusAccepted)
	})
}

func (s *codexSetup) start(helper, action string, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.view.State {
	case "preparing", "authorize", "running_sample":
		return false
	}
	s.view = codexSetupView{State: "preparing"}
	if action == "run" {
		s.view = codexSetupView{State: "running_sample", TaskID: id}
	}
	go s.run(helper, action, id)
	return true
}

func (s *codexSetup) set(view codexSetupView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.view = view
}

func (s *codexSetup) run(helper, action string, id int64) {
	timeout := 20 * time.Minute
	if action == "check" {
		timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := []string{helper, action}
	if action == "run" {
		args = append(args, strconv.FormatInt(id, 10))
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", args...)
	reader, writer := io.Pipe()
	defer reader.Close()
	cmd.Stdout, cmd.Stderr = writer, writer
	cmd.WaitDelay = 2 * time.Second
	finished := make(chan error, 1)
	go func() {
		finished <- cmd.Run()
		writer.Close()
	}()
	message := ""
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 256<<10)
	sawDeviceURL, nextLineIsCode := false, false
	for scanner.Scan() {
		line := strings.TrimSpace(terminalColor.ReplaceAllString(scanner.Text(), ""))
		switch {
		case nextLineIsCode:
			if deviceCode.MatchString(line) {
				s.set(codexSetupView{State: "authorize", URL: deviceSignInURL, Code: line})
			}
			nextLineIsCode = false
		case action == "prepare" && line == deviceSignInURL:
			sawDeviceURL = true
		case sawDeviceURL && strings.HasPrefix(line, "2. Enter this one-time code"):
			sawDeviceURL, nextLineIsCode = false, true
		case strings.HasPrefix(line, "Difqo worker setup: ") && len(line) < 256:
			message = strings.TrimPrefix(line, "Difqo worker setup: ")
		}
	}
	// Drain any output after an oversized line, then collect the exit status.
	_, _ = io.Copy(io.Discard, reader)
	if err := <-finished; err == nil && scanner.Err() == nil {
		s.set(codexSetupView{State: "ready"})
		return
	}
	if message == "" {
		message = "Couldn’t finish Codex setup. Check Docker Desktop and whether device-code sign-in is enabled in your ChatGPT security settings, then retry."
	}
	s.set(codexSetupView{State: "attention", Message: message, TaskID: id})
}
