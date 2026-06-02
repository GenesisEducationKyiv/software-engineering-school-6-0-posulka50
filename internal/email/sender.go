package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/posul/github-notifier/internal/metrics"
)

type ConfirmData struct {
	Repo       string
	ConfirmURL string
}

type ReleaseData struct {
	Repo           string
	TagName        string
	ReleaseName    string
	Body           string
	ReleaseURL     string
	UnsubscribeURL string
}

// renderer defines the interface for rendering email HTML bodies.
type renderer interface {
	RenderConfirmation(data ConfirmData) (string, error)
	RenderRelease(data ReleaseData) (string, error)
}

type Sender struct {
	httpClient *http.Client
	apiURL     string
	apiKey     string
	from       string
	renderer   renderer
}

func NewSender(apiKey, from, apiURL string, r renderer) *Sender {
	return &Sender{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiURL:     apiURL,
		apiKey:     apiKey,
		from:       from,
		renderer:   r,
	}
}

func (s *Sender) SendConfirmation(ctx context.Context, to string, data ConfirmData) error {
	body, err := s.renderer.RenderConfirmation(data)
	if err != nil {
		return err
	}
	err = s.send(ctx, to, fmt.Sprintf("Confirm your subscription to %s releases", data.Repo), body)
	if err == nil {
		metrics.EmailsSentTotal.WithLabelValues("confirmation").Inc()
	}
	return err
}

func (s *Sender) SendReleaseNotification(ctx context.Context, to string, data ReleaseData) error {
	body, err := s.renderer.RenderRelease(data)
	if err != nil {
		return err
	}
	err = s.send(ctx, to, fmt.Sprintf("[%s] New release: %s", data.Repo, data.TagName), body)
	if err == nil {
		metrics.EmailsSentTotal.WithLabelValues("release").Inc()
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
		return fmt.Errorf("send resend request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("resend returned status %d", resp.StatusCode)
	}
	return nil
}
