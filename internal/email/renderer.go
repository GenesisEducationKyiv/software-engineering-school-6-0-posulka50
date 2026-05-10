package email

import (
	"bytes"
	"fmt"
	"html/template"
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

// TemplateRenderer renders email HTML bodies from embedded templates.
type TemplateRenderer struct{}

// NewTemplateRenderer creates a new TemplateRenderer.
func NewTemplateRenderer() *TemplateRenderer {
	return &TemplateRenderer{}
}

// RenderConfirmation renders the confirmation email HTML body.
func (r *TemplateRenderer) RenderConfirmation(data ConfirmData) (string, error) {
	return renderTemplate(confirmTmpl, data)
}

// RenderRelease renders the release notification email HTML body.
func (r *TemplateRenderer) RenderRelease(data ReleaseData) (string, error) {
	return renderTemplate(releaseTmpl, data)
}

func renderTemplate(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return buf.String(), nil
}
