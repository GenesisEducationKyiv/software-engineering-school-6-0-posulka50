package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
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

const resendAPIURL = "https://api.resend.com/emails"

type Notifier interface {
	SendConfirmation(ctx context.Context, to string, data ConfirmData) error
	SendReleaseNotification(ctx context.Context, to string, data ReleaseData) error
}

var (
	baseTmpl    = mustLoadTemplate(nil, "templates/base.html")
	confirmTmpl = mustLoadTemplate(baseTmpl, "templates/confirm.html")
	releaseTmpl = mustLoadTemplate(baseTmpl, "templates/release.html")
)

func mustLoadTemplate(base *template.Template, path string) *template.Template {
	content, err := templateFS.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("read template %s: %v", path, err))
	}
	if base != nil {
		return template.Must(template.Must(base.Clone()).Parse(string(content)))
	}
	return template.Must(template.New("base").Parse(string(content)))
}

type Sender struct {
	httpClient *http.Client
	apiKey     string
	from       string
}

func NewSender(apiKey, from string) *Sender {
	return &Sender{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiKey:     apiKey,
		from:       from,
	}
}

func (s *Sender) SendConfirmation(ctx context.Context, to string, data ConfirmData) error {
	body, err := renderTemplate(confirmTmpl, data)
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
	body, err := renderTemplate(releaseTmpl, data)
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

func renderTemplate(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return buf.String(), nil
}
