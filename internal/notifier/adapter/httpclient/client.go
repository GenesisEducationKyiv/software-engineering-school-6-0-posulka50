package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/posul/github-notifier/internal/notifier/domain"
)

// Client calls the notifier service over HTTP. It satisfies both the
// subscription-side confirmationSender port and the release-side releaseSender
// port via structural typing.
type Client struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
}

func NewClient(baseURL, authToken string) *Client {
	return &Client{
		baseURL:    baseURL,
		authToken:  authToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) SendConfirmation(ctx context.Context, to, repo, confirmURL string) error {
	return c.post(ctx, "/v1/notifications/confirmation", map[string]string{
		"to":          to,
		"repo":        repo,
		"confirm_url": confirmURL,
	})
}

func (c *Client) SendReleaseNotification(ctx context.Context, to string, data domain.ReleaseData) error {
	return c.post(ctx, "/v1/notifications/release", map[string]string{
		"to":              to,
		"repo":            data.Repo,
		"tag_name":        data.TagName,
		"release_name":    data.ReleaseName,
		"body":            data.Body,
		"release_url":     data.ReleaseURL,
		"unsubscribe_url": data.UnsubscribeURL,
	})
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal notifier request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create notifier request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("X-Internal-Token", c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call notifier: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("notifier returned status %d", resp.StatusCode)
	}
	return nil
}
