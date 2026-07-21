package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/posul/github-notifier/internal/notifier/domain"
	"github.com/posul/github-notifier/internal/platform/metrics"
)

// Renderer renders email bodies for the Sender. It is satisfied by
// notifier/adapter/templates.Renderer.
type Renderer interface {
	RenderConfirmation(data domain.ConfirmData) (string, error)
	RenderRelease(data domain.ReleaseData) (string, error)
}

type Sender struct {
	httpClient *http.Client
	apiURL     string
	apiKey     string
	from       string
	renderer   Renderer
}

func NewSender(apiKey, from, apiURL string, r Renderer) *Sender {
	return &Sender{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiURL:     apiURL,
		apiKey:     apiKey,
		from:       from,
		renderer:   r,
	}
}

func (s *Sender) SendConfirmation(ctx context.Context, to string, data domain.ConfirmData) error {
	body, err := s.renderer.RenderConfirmation(data)
	if err != nil {
		return err
	}
	err = s.send(ctx, to, fmt.Sprintf("Confirm your subscription to %s releases", data.Repo), body)
	if err == nil {
		metrics.EmailsSentTotal.WithLabelValues("confirmation").Inc()
	} else {
		metrics.EmailSendErrorsTotal.WithLabelValues("confirmation").Inc()
	}
	return err
}

func (s *Sender) SendReleaseNotification(ctx context.Context, to string, data domain.ReleaseData) error {
	body, err := s.renderer.RenderRelease(data)
	if err != nil {
		return err
	}
	err = s.send(ctx, to, fmt.Sprintf("[%s] New release: %s", data.Repo, data.TagName), body)
	if err == nil {
		metrics.EmailsSentTotal.WithLabelValues("release").Inc()
	} else {
		metrics.EmailSendErrorsTotal.WithLabelValues("release").Inc()
	}
	return err
}

func (s *Sender) send(ctx context.Context, to, subject, htmlBody string) error {
	payload, err := json.Marshal(map[string]any{
		"from":    s.from,
		"to":      []string{to},
		"subject": subject,
		"html":    htmlBody,
	})
	if err != nil {
		return fmt.Errorf("marshal resend payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// Network / context-deadline failures are transient upstream problems.
		// Wrap so gRPC callers can surface codes.Unavailable /
		// codes.DeadlineExceeded (context.DeadlineExceeded stays reachable
		// through the joined chain).
		return fmt.Errorf("send resend request: %w", errors.Join(err, domain.ErrUpstreamUnavailable))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("resend returned status %d: %w", resp.StatusCode, domain.ErrUpstreamUnavailable)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("resend returned status %d", resp.StatusCode)
	}
	return nil
}
