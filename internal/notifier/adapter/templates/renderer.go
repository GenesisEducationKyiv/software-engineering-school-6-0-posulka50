package templates

import (
	"bytes"
	"fmt"
	"html/template"

	"github.com/posul/github-notifier/internal/notifier/domain"
)

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

// Renderer renders email HTML bodies from embedded templates.
type Renderer struct{}

// NewRenderer creates a new Renderer.
func NewRenderer() *Renderer {
	return &Renderer{}
}

// RenderConfirmation renders the confirmation email HTML body.
func (r *Renderer) RenderConfirmation(data domain.ConfirmData) (string, error) {
	return renderTemplate(confirmTmpl, data)
}

// RenderRelease renders the release notification email HTML body.
func (r *Renderer) RenderRelease(data domain.ReleaseData) (string, error) {
	return renderTemplate(releaseTmpl, data)
}

func renderTemplate(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return buf.String(), nil
}
