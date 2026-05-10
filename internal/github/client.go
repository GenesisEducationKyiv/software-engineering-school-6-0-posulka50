package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/posul/github-notifier/internal/metrics"
)

var (
	// ErrNotFound is returned when a repository does not exist on GitHub.
	ErrNotFound = errors.New("repository not found")

	// ErrRateLimit is returned when the GitHub API rate limit is exceeded.
	ErrRateLimit = errors.New("github api rate limit exceeded")
)

// RepoChecker checks whether a GitHub repository exists.
type RepoChecker interface {
	CheckRepo(ctx context.Context, owner, repo string) error
}

// Release represents a GitHub repository release.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
}

// Client is a pure HTTP client for the GitHub REST API.
type Client struct {
	httpClient *http.Client
	token      string
}

// NewClient creates a new GitHub API client.
func NewClient(token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		token:      token,
	}
}

// CheckRepo verifies that the given owner/repo exists on GitHub.
func (c *Client) CheckRepo(ctx context.Context, owner, repo string) error {
	log.Printf("github: check repo %s/%s", owner, repo)
	resp, err := c.doRequest(ctx, fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo))
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("github: failed to close response body: %v", err)
		}
	}()
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		metrics.GithubAPIRequestsTotal.WithLabelValues("check_repo", "ok").Inc()
		return nil
	case http.StatusNotFound:
		metrics.GithubAPIRequestsTotal.WithLabelValues("check_repo", "not_found").Inc()
		return ErrNotFound
	case http.StatusTooManyRequests:
		metrics.GithubAPIRequestsTotal.WithLabelValues("check_repo", "rate_limit").Inc()
		return ErrRateLimit
	default:
		metrics.GithubAPIRequestsTotal.WithLabelValues("check_repo", "error").Inc()
		return fmt.Errorf("github api returned status %d", resp.StatusCode)
	}
}

// GetLatestRelease returns the latest release for the given owner/repo.
func (c *Client) GetLatestRelease(ctx context.Context, owner, repo string) (*Release, error) {
	log.Printf("github: fetch latest release %s/%s", owner, repo)
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	resp, err := c.doRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("github: failed to close response body: %v", err)
		}
	}()

	switch resp.StatusCode {
	case http.StatusNotFound:
		metrics.GithubAPIRequestsTotal.WithLabelValues("get_release", "not_found").Inc()
		return nil, ErrNotFound
	case http.StatusTooManyRequests:
		metrics.GithubAPIRequestsTotal.WithLabelValues("get_release", "rate_limit").Inc()
		return nil, ErrRateLimit
	case http.StatusOK:
		metrics.GithubAPIRequestsTotal.WithLabelValues("get_release", "ok").Inc()
	default:
		metrics.GithubAPIRequestsTotal.WithLabelValues("get_release", "error").Inc()
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	log.Printf("github: got release %s/%s → %s", owner, repo, release.TagName)
	return &release, nil
}

func (c *Client) doRequest(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "github-notifier/1.0")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.httpClient.Do(req)
}
