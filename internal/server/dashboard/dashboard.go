// Package dashboard serves the HTML UI and JSON API that read
// completed investigations out of storage.
package dashboard

import (
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/trialanderror-eng/lolo/internal/storage"
)

//go:embed templates/*.html
var templatesFS embed.FS

const listLimit = 100

type server struct {
	store      storage.Storage
	indexTmpl  *template.Template
	detailTmpl *template.Template
}

// Register attaches the dashboard's routes to mux.
func Register(mux *http.ServeMux, store storage.Storage) {
	idx, det := parseTemplates()
	s := &server{store: store, indexTmpl: idx, detailTmpl: det}
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /investigations/{id}", s.detail)
	mux.HandleFunc("POST /investigations/{id}/resolve", s.resolve)
	mux.HandleFunc("GET /api/investigations", s.apiList)
	mux.HandleFunc("GET /api/investigations/{id}", s.apiGet)
}

// parseTemplates returns two independent template trees so the page-specific
// {{define "title"}} / {{define "content"}} blocks don't collide.
func parseTemplates() (*template.Template, *template.Template) {
	funcs := template.FuncMap{
		"scoreClass": func(s float64) string {
			switch {
			case s >= 0.8:
				return "bg-red-900/60 text-red-200"
			case s >= 0.5:
				return "bg-amber-900/60 text-amber-200"
			default:
				return "bg-slate-800 text-slate-400"
			}
		},
		"add": func(a, b int) int { return a + b },
	}
	index := template.Must(template.New("layout.html").Funcs(funcs).ParseFS(templatesFS,
		"templates/layout.html", "templates/index.html"))
	detail := template.Must(template.New("layout.html").Funcs(funcs).ParseFS(templatesFS,
		"templates/layout.html", "templates/detail.html"))
	return index, detail
}

type indexData struct {
	Now            time.Time
	Investigations []storage.Investigation
}

type detailData struct {
	Now           time.Time
	Investigation storage.Investigation
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	invs, err := s.store.List(r.Context(), listLimit)
	if err != nil {
		http.Error(w, "storage list: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.indexTmpl.ExecuteTemplate(w, "layout.html", indexData{
		Now:            time.Now(),
		Investigations: invs,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "storage get: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.detailTmpl.ExecuteTemplate(w, "layout.html", detailData{
		Now:           time.Now(),
		Investigation: inv,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) resolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	notes := strings.TrimSpace(r.PostForm.Get("notes"))
	if notes == "" {
		http.Error(w, "notes is required", http.StatusBadRequest)
		return
	}

	inv, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	user, _, _ := r.BasicAuth()
	inv.Resolution = storage.Resolution{
		ResolvedAt: time.Now().UTC(),
		Notes:      notes,
		ResolvedBy: user,
	}
	if err := s.store.Save(r.Context(), inv); err != nil {
		http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// POST/Redirect/GET — reloading the detail page shouldn't re-submit.
	http.Redirect(w, r, "/investigations/"+id, http.StatusSeeOther)
}

func (s *server) apiList(w http.ResponseWriter, r *http.Request) {
	invs, err := s.store.List(r.Context(), listLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"investigations": invs})
}

func (s *server) apiGet(w http.ResponseWriter, r *http.Request) {
	inv, ok, err := s.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, inv)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
