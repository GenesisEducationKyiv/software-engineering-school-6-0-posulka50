package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/posul/github-notifier/internal/metrics"
)

// ReleaseFetcherClient fetches the latest GitHub release via the REST API.
type ReleaseFetcherClient struct {
	httpClient *http.Client
	token      string
}

func NewReleaseFetcherClient(token string) *ReleaseFetcherClient {
	return &ReleaseFetcherClient{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		token:      token,
	}
}

func (c *ReleaseFetcherClient) GetLatestRelease(ctx context.Context, owner, repo string) (*Release, error) {
	log.Printf("github: fetch latest release %s/%s", owner, repo)
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	resp, err := doRequest(ctx, c.httpClient, c.token, url)
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

	log.Printf("github: got release %s/%s -> %s", owner, repo, release.TagName)
	return &release, nil
}
