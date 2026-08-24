package web

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/index.html static/style.css static/app.js
var assets embed.FS

func Handler() (http.Handler, error) {
	page, err := template.ParseFS(assets, "templates/index.html")
	if err != nil {
		return nil, err
	}
	static := http.FileServer(http.FS(assets))
	mux := http.NewServeMux()
	mux.Handle("/static/", static)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := page.Execute(w, nil); err != nil {
			http.Error(w, "template execution failed", http.StatusInternalServerError)
		}
	})
	return mux, nil
}
