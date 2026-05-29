package github

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/posul/github-notifier/internal/metrics"
)

// RepoCheckerClient checks GitHub repository existence via the REST API.
type RepoCheckerClient struct {
	httpClient *http.Client
	token      string
}

func NewRepoCheckerClient(token string) *RepoCheckerClient {
	return &RepoCheckerClient{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		token:      token,
	}
}

func (c *RepoCheckerClient) CheckRepo(ctx context.Context, owner, repo string) error {
	slog.Debug("github: check repo", "owner", owner, "repo", repo)
	resp, err := doRequest(ctx, c.httpClient, c.token, fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo))
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("github: failed to close response body", "error", err)
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
