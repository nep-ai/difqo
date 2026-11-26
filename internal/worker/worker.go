package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/nep-ai/difqo/internal/execution"
	"github.com/nep-ai/difqo/internal/store"
)

func Run(ctx context.Context, address string, repo string, executionOptions *execution.Options, githubRepositoryID, taskID int64, project store.SpecialistProject) error {
	client := &http.Client{Timeout: 10 * time.Second}
	var task store.Task
	endpoint := "/tasks/claim"
	var claim any = struct {
		GitHubRepositoryID int64 `json:"github_repository_id,omitempty"`
		TaskID             int64 `json:"task_id,omitempty"`
	}{githubRepositoryID, taskID}
	if project.ID != "" {
		endpoint = "/specialist-runs/claim"
		claim = struct {
			Project string `json:"project"`
			Binding string `json:"binding"`
		}{Project: project.ID, Binding: project.Binding}
	}
	err := request(ctx, client, "POST", address+endpoint, claim, &task)
	if err != nil {
		return err
	}
	if task.Attempt == nil {
		if taskID != 0 {
			return errors.New("the selected goal is unavailable for a local claim; check its status in Chief of Staff")
		}
		return nil
	}

	attemptURL := fmt.Sprintf("%s/attempts/%d", address, task.Attempt.ID)
	var result store.Result
	if project.ID != "" {
		if task.Run == nil || task.Run.Project.ID != project.ID || task.Run.Project.Binding != project.Binding {
			return errors.New("claimed run does not match the worker project binding")
		}
		result = execution.ReviewProjectSnapshot(ctx, repo, task)
	} else if executionOptions == nil {
		result = checkHead(ctx, repo)
	} else {
		runCtx, cancel := context.WithTimeout(ctx, 210*time.Second)
		defer cancel()
		heartbeat := make(chan error, 1)
		go func() {
			err := renewLease(runCtx, client, attemptURL)
			heartbeat <- err
			if err != nil {
				cancel()
			}
		}()
		result = executionOptions.Run(runCtx, repo, task)
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			result.ExitCode = 124
			result.Output = "Codex execution exceeded the host deadline"
			result.Blocked = false
		}
		cancel()
		if err := <-heartbeat; err != nil {
			return err
		}
	}
	return request(ctx, client, "PUT", attemptURL+"/result", result, nil)
}

func renewLease(ctx context.Context, client *http.Client, attemptURL string) error {
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := request(ctx, client, "POST", attemptURL+"/renew", struct{}{}, nil); err != nil && ctx.Err() == nil {
				return err
			}
		}
	}
}

func checkHead(ctx context.Context, repo string) store.Result {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, "git", "-C", repo, "rev-parse", "--verify", "HEAD").Output()
	result := store.Result{ExitCode: 0, Output: strings.TrimSpace(string(output))}
	if err != nil {
		result = store.Result{ExitCode: 125, Output: "could not read the repository HEAD"}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() >= 0 {
			result.ExitCode = exit.ExitCode()
		}
	}
	return result
}

func request(ctx context.Context, client *http.Client, method, url string, body, dst any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusNoContent:
		return nil
	case res.StatusCode == http.StatusOK && dst != nil:
		return json.NewDecoder(res.Body).Decode(dst)
	default:
		return errors.New(method + " " + req.URL.Path + ": " + res.Status)
	}
}
