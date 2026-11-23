// Package githubintake imports authorized GitHub conversation comments into the local task queue.
package githubintake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
)

const apiURL = "https://api.github.com"

var (
	repositoryName = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9_.-]+$`)
	appHandle      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
)

type Config struct {
	Enrollment store.GitHubEnrollment
	Repository string
	Handle     string
	Token      string
}

type taskStore interface {
	GitHubCursor(context.Context, store.GitHubEnrollment) (time.Time, error)
	ImportGitHub(context.Context, store.GitHubEnrollment, time.Time, []store.GitHubTask) (int, error)
}

type comment struct {
	ID        int64     `json:"id"`
	IssueURL  string    `json:"issue_url"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"user"`
}

func Poll(ctx context.Context, config Config, tasks store.Tasks) (int, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return poll(ctx, client, apiURL, config, tasks)
}

func poll(ctx context.Context, client *http.Client, base string, config Config, tasks taskStore) (int, error) {
	enrollment := config.Enrollment
	if enrollment.OwnerID <= 0 || enrollment.RepositoryID <= 0 ||
		enrollment.Start.IsZero() || enrollment.Start.After(time.Now()) ||
		!repositoryName.MatchString(config.Repository) || !appHandle.MatchString(config.Handle) ||
		strings.TrimSpace(config.Token) == "" {
		return 0, errors.New("GitHub intake requires an enrolled repository, owner ID, start time, app handle, and installation token")
	}
	repoName := strings.Split(config.Repository, "/")[1]
	if repoName == "." || repoName == ".." {
		return 0, errors.New("invalid GitHub repository name")
	}
	cursor, err := tasks.GitHubCursor(ctx, enrollment)
	if err != nil {
		return 0, err
	}

	// This endpoint accepts installation tokens and verifies that the selected
	// repository is within this token's scope, including for public repos.
	var installation struct {
		Repositories []struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
		} `json:"repositories"`
	}
	if _, err := get(ctx, client, base+"/installation/repositories?per_page=100", config.Token, &installation); err != nil {
		return 0, err
	}
	var repo string
	for _, candidate := range installation.Repositories {
		if candidate.ID == enrollment.RepositoryID && strings.EqualFold(candidate.FullName, config.Repository) {
			repo = candidate.FullName
			break
		}
	}
	if repo == "" {
		return 0, errors.New("GitHub installation does not expose the enrolled repository on the first page; reconnect with a token scoped to that repository")
	}

	repoURL := base + "/repos/" + repo
	query := url.Values{
		"sort": {"updated"}, "direction": {"asc"}, "per_page": {"100"},
		// GitHub's since filter is exclusive and timestamps have second precision.
		"since": {cursor.Add(-time.Second).UTC().Format(time.RFC3339)},
	}
	var comments []comment
	more, err := get(ctx, client, repoURL+"/issues/comments?"+query.Encode(), config.Token, &comments)
	if err != nil {
		return 0, err
	}
	if more || len(comments) > 100 {
		return 0, errors.New("GitHub intake window exceeds one page; cursor unchanged")
	}
	if comments == nil {
		return 0, errors.New("GitHub returned an invalid comment batch")
	}
	var accepted []store.GitHubTask
	for _, c := range comments {
		if c.ID <= 0 || c.CreatedAt.IsZero() || c.UpdatedAt.Before(c.CreatedAt) {
			return 0, errors.New("GitHub returned invalid comment metadata")
		}
		if c.UpdatedAt.After(cursor) {
			cursor = c.UpdatedAt
		}
		if c.User.ID != enrollment.OwnerID || c.User.Type != "User" || c.CreatedAt.Before(enrollment.Start) {
			continue
		}
		goal := mentionedGoal(c.Body, config.Handle)
		if goal == "" {
			continue
		}
		thread, ok := issueNumber(c.IssueURL, repoURL, base+"/repositories/"+strconv.FormatInt(enrollment.RepositoryID, 10))
		if !ok {
			return 0, errors.New("GitHub returned a comment outside the enrolled repository")
		}
		accepted = append(accepted, store.GitHubTask{CommentID: c.ID, ThreadNumber: thread, Goal: goal})
	}
	return tasks.ImportGitHub(ctx, enrollment, cursor, accepted)
}

func mentionedGoal(body, handle string) string {
	body = strings.TrimSpace(body)
	space := strings.IndexFunc(body, unicode.IsSpace)
	if space < 0 || !strings.EqualFold(body[:space], "@"+handle) {
		return ""
	}
	goal := strings.TrimSpace(body[space:])
	if utf8.RuneCountInString(goal) > 4096 || strings.ContainsRune(goal, 0) {
		return ""
	}
	return goal
}

func issueNumber(issueURL, repoURL, repoIDURL string) (int64, bool) {
	for _, prefix := range []string{repoURL, repoIDURL} {
		number, ok := strings.CutPrefix(issueURL, prefix+"/issues/")
		if ok {
			id, err := strconv.ParseInt(number, 10, 64)
			return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == number
		}
	}
	return 0, false
}

func get(ctx context.Context, client *http.Client, address, token string, dst any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false, errors.New("could not prepare GitHub request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, errors.New("could not reach GitHub")
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		switch {
		case res.StatusCode == http.StatusTooManyRequests || res.Header.Get("Retry-After") != "" ||
			res.Header.Get("X-RateLimit-Remaining") == "0":
			return false, errors.New("GitHub rate limited intake; retry after the limit resets")
		case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden ||
			res.StatusCode == http.StatusNotFound:
			return false, errors.New("GitHub access is unavailable; reconnect the installation and supply a fresh scoped token")
		default:
			return false, fmt.Errorf("GitHub intake request failed with HTTP %d", res.StatusCode)
		}
	}
	const maxBody = 8 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(body) > maxBody || json.Unmarshal(body, dst) != nil {
		return false, errors.New("could not read the bounded GitHub response")
	}
	for _, link := range res.Header.Values("Link") {
		if strings.Contains(link, `rel="next"`) {
			return true, nil
		}
	}
	return false, nil
}
