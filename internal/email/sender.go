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

// Notifier defines the interface for sending email notifications.
type Notifier interface {
	SendConfirmation(to string, data ConfirmData) error
	SendReleaseNotification(to string, data ReleaseData) error
}

var baseTmpl = template.Must(template.New("base").Parse(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
</head>
<body style="margin:0;padding:0;background:#0d1117;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
  <table width="100%" cellpadding="0" cellspacing="0" style="padding:40px 16px;">
    <tr><td align="center">
      <table width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#161b22;border:1px solid #30363d;border-radius:12px;">
        <tr><td style="padding:36px 40px;">

          <table cellpadding="0" cellspacing="0" style="margin-bottom:28px;">
            <tr>
              <td style="vertical-align:middle;padding-right:10px;">
                <svg width="22" height="22" viewBox="0 0 16 16" fill="#e6edf3" xmlns="http://www.w3.org/2000/svg">
                  <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/>
                </svg>
              </td>
              <td style="vertical-align:middle;font-size:15px;font-weight:600;color:#e6edf3;">Release Notifier</td>
            </tr>
          </table>

          {{block "content" .}}{{end}}

          <hr style="margin:28px 0;border:none;border-top:1px solid #21262d;">
          <p style="margin:0;font-size:12px;color:#484f58;line-height:1.6;">{{block "footer" .}}{{end}}</p>

        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`))

var confirmTmpl = template.Must(template.Must(baseTmpl.Clone()).Parse(`
{{define "content"}}
  <p style="margin:0 0 8px;font-size:12px;color:#8b949e;text-transform:uppercase;letter-spacing:.08em;font-weight:600;">Confirm subscription</p>
  <h1 style="margin:0 0 16px;font-size:22px;font-weight:700;color:#e6edf3;">{{.Repo}}</h1>
  <p style="margin:0 0 28px;font-size:15px;color:#8b949e;line-height:1.6;">
    You requested release notifications for <strong style="color:#e6edf3;">{{.Repo}}</strong>.
    Click the button below to confirm your subscription.
  </p>
  <a href="{{.ConfirmURL}}" style="display:inline-block;background:#238636;color:#fff;padding:12px 28px;text-decoration:none;border-radius:6px;font-size:14px;font-weight:600;border:1px solid #2ea043;">Confirm Subscription</a>
  <p style="margin:20px 0 0;font-size:12px;color:#484f58;">
    Or copy this link:<br>
    <a href="{{.ConfirmURL}}" style="color:#58a6ff;word-break:break-all;">{{.ConfirmURL}}</a>
  </p>
{{end}}
{{define "footer"}}If you didn't request this, you can safely ignore this email.{{end}}`))

var releaseTmpl = template.Must(template.Must(baseTmpl.Clone()).Parse(`
{{define "content"}}
  <p style="margin:0 0 8px;font-size:12px;color:#8b949e;text-transform:uppercase;letter-spacing:.08em;font-weight:600;">New release · {{.Repo}}</p>
  <h1 style="margin:0 0 6px;font-size:22px;font-weight:700;color:#e6edf3;">{{.TagName}}</h1>
  {{if .ReleaseName}}<p style="margin:0 0 20px;font-size:16px;font-weight:600;color:#c9d1d9;">{{.ReleaseName}}</p>{{else}}<p style="margin:0 0 20px;"></p>{{end}}
  {{if .Body}}<div style="background:#0d1117;border:1px solid #21262d;border-radius:6px;padding:16px;margin:0 0 24px;font-size:13px;color:#8b949e;line-height:1.7;white-space:pre-wrap;word-break:break-word;">{{.Body}}</div>{{end}}
  <a href="{{.ReleaseURL}}" style="display:inline-block;background:#1f6feb;color:#fff;padding:12px 28px;text-decoration:none;border-radius:6px;font-size:14px;font-weight:600;border:1px solid #388bfd;">View Release on GitHub</a>
{{end}}
{{define "footer"}}<a href="{{.UnsubscribeURL}}" style="color:#484f58;">Unsubscribe</a> from {{.Repo}} release notifications.{{end}}`))

// Sender sends transactional emails via the Resend API.
type Sender struct {
	httpClient *http.Client
	apiKey     string
	from       string
}

// NewSender creates a new Sender using the given Resend API key and sender address.
func NewSender(apiKey, from string) *Sender {
	return &Sender{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiKey:     apiKey,
		from:       from,
	}
}

// SendConfirmation sends a subscription confirmation email to the given address.
func (s *Sender) SendConfirmation(to string, data ConfirmData) error {
	body, err := renderTemplate(confirmTmpl, data)
	if err != nil {
		return err
	}
	err = s.send(context.Background(), to, fmt.Sprintf("Confirm your subscription to %s releases", data.Repo), body)
	if err == nil {
		metrics.EmailsSentTotal.WithLabelValues("confirmation").Inc()
	}
	return err
}

// SendReleaseNotification sends a new release notification email to the given address.
func (s *Sender) SendReleaseNotification(to string, data ReleaseData) error {
	body, err := renderTemplate(releaseTmpl, data)
	if err != nil {
		return err
	}
	err = s.send(context.Background(), to, fmt.Sprintf("[%s] New release: %s", data.Repo, data.TagName), body)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
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
