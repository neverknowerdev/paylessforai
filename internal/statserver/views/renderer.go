// Package views renders stat-server HTML from embedded templates.
package views

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
)

//go:embed templates/*.html
var files embed.FS

type Renderer struct{ templates *template.Template }

func New() (*Renderer, error) {
	templates, err := template.ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse views: %w", err)
	}
	return &Renderer{templates: templates}, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return r.templates.ExecuteTemplate(w, name, data)
}
