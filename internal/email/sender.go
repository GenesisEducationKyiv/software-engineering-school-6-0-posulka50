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

// ConfirmData holds the template data for subscription confirmation emails.
type ConfirmData struct {
	Repo       string
	ConfirmURL string
}

// ReleaseData holds the template data for release notification emails.
type ReleaseData struct {
	Repo           string
	TagName        string
	ReleaseName    string
	Body           string
	ReleaseURL     string
	UnsubscribeURL string
}

const resendAPIURL = "https://api.resend.com/emails"

// Notifier defines the interface for sending email notifications.
type Notifier interface {
	SendConfirmation(ctx context.Context, to string, data ConfirmData) error
	SendReleaseNotification(ctx context.Context, to string, data ReleaseData) error
}

// renderer defines the interface for rendering email HTML bodies.
type renderer interface {
	RenderConfirmation(data ConfirmData) (string, error)
	RenderRelease(data ReleaseData) (string, error)
}

// Sender sends transactional emails via the Resend API.
type Sender struct {
	httpClient *http.Client
	apiKey     string
	from       string
	renderer   renderer
}

// NewSender creates a new Sender using the given Resend API key and sender address.
func NewSender(apiKey, from string) *Sender {
	return &Sender{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiKey:     apiKey,
		from:       from,
		renderer:   NewTemplateRenderer(),
	}
}

// SendConfirmation sends a subscription confirmation email to the given address.
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

// SendReleaseNotification sends a new release notification email to the given address.
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIURL, bytes.NewReader(payload))
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
