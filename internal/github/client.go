package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	// ErrNotFound is returned when a repository does not exist on GitHub.
	ErrNotFound = errors.New("repository not found")

	ErrRateLimit = errors.New("github api rate limit exceeded")
)

type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
}

// RepoChecker checks whether a GitHub repository exists.
type RepoChecker interface {
	CheckRepo(ctx context.Context, owner, repo string) error
}

// ReleaseFetcher fetches the latest release for a GitHub repository.
type ReleaseFetcher interface {
	GetLatestRelease(ctx context.Context, owner, repo string) (*Release, error)
}

func doRequest(ctx context.Context, c *http.Client, token, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "github-notifier/1.0")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.Do(req)
}
