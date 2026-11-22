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

	"github.com/nep-ai/difqo/internal/store"
)

func Run(ctx context.Context, address string, repo string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	var task store.Task
	err := request(ctx, client, "POST", address+"/tasks/claim", struct{}{}, &task)
	if err != nil || task.Attempt == nil {
		return err
	}

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
	return request(ctx, client, "PUT", fmt.Sprintf("%s/attempts/%d/result", address, task.Attempt.ID), result, nil)
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
