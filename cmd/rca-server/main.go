package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
	"github.com/trialanderror-eng/lolo/internal/investigator"
	"github.com/trialanderror-eng/lolo/internal/investigators/deploys"
	k8sinv "github.com/trialanderror-eng/lolo/internal/investigators/kubernetes"
	"github.com/trialanderror-eng/lolo/internal/investigators/logs"
	meminv "github.com/trialanderror-eng/lolo/internal/investigators/memory"
	"github.com/trialanderror-eng/lolo/internal/investigators/prometheus"
	"github.com/trialanderror-eng/lolo/internal/output/slack"
	"github.com/trialanderror-eng/lolo/internal/output/stdout"
	"github.com/trialanderror-eng/lolo/internal/server/dashboard"
	"github.com/trialanderror-eng/lolo/internal/storage"
	"github.com/trialanderror-eng/lolo/internal/storage/memory"
	sqlitestore "github.com/trialanderror-eng/lolo/internal/storage/sqlite"
	"github.com/trialanderror-eng/lolo/internal/trigger/alertmanager"
	"github.com/trialanderror-eng/lolo/internal/trigger/manual"
)

func main() {
	addr := flag.String("addr", envOr("LOLO_ADDR", ":8080"), "listen address")
	flag.Parse()

	store := openStorage()

	var invs []investigator.Investigator
	invs = append(invs, meminv.New(store, os.Getenv("LOLO_PUBLIC_URL")))
	if token := os.Getenv("LOLO_GITHUB_TOKEN"); token != "" {
		invs = append(invs, deploys.New(token, splitCSV(os.Getenv("LOLO_GITHUB_REPOS"))))
	}
	invs = append(invs, k8sinv.New(splitCSV(os.Getenv("LOLO_K8S_NAMESPACES"))))
	if promURL := os.Getenv("LOLO_PROMETHEUS_URL"); promURL != "" {
		invs = append(invs, prometheus.New(promURL, os.Getenv("LOLO_PROMETHEUS_TOKEN")))
	}
	if lokiURL := os.Getenv("LOLO_LOKI_URL"); lokiURL != "" {
		invs = append(invs, logs.New(lokiURL, os.Getenv("LOLO_LOKI_TOKEN")))
	}

	sinks := []Sink{stdout.New()}
	if url := os.Getenv("LOLO_SLACK_WEBHOOK_URL"); url != "" {
		sinks = append(sinks, slack.New(url))
	}

	engine := &engine{
		investigators: invs,
		ranker:        hypothesis.CorrelatingRanker{TopN: 10},
		sinks:         sinks,
		storage:       store,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/webhook/alertmanager", engine.handleAlertmanager)
	mux.HandleFunc("/investigate", engine.handleManual)
	dashboard.Register(mux, store)

	webhookToken := os.Getenv("LOLO_WEBHOOK_TOKEN")
	if webhookToken == "" {
		log.Printf("WARNING: LOLO_WEBHOOK_TOKEN unset — /webhook/* endpoints accept unauthenticated requests (dev mode)")
	}
	dashboardToken := os.Getenv("LOLO_DASHBOARD_TOKEN")
	if dashboardToken == "" {
		log.Printf("WARNING: LOLO_DASHBOARD_TOKEN unset — dashboard and /api/* accept unauthenticated requests (dev mode)")
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           authMiddleware(webhookToken, dashboardToken, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("lolo listening on %s", *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// Sink is the server-local view of output.Sink. The canonical interface lives
// next to each implementation; this mirror exists so main can hold a []Sink.
type Sink interface {
	Emit(ctx context.Context, inc incident.Incident, hs []hypothesis.Hypothesis) error
}

type engine struct {
	investigators []investigator.Investigator
	ranker        hypothesis.Ranker
	sinks         []Sink
	storage       storage.Storage
}

func (e *engine) handleAlertmanager(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	inc, err := alertmanager.Parse(r.Body)
	if err != nil {
		http.Error(w, "bad payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	e.runAndRespond(w, r, inc)
}

func (e *engine) handleManual(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	inc, err := manual.Parse(r.Body)
	if err != nil {
		http.Error(w, "bad payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	e.runAndRespond(w, r, inc)
}

// runAndRespond is the shared post-parse path for every trigger: run
// investigators → rank → persist → emit sinks → reply. Keeps the
// handlers thin and means every new trigger gets the same storage +
// sink guarantees for free.
func (e *engine) runAndRespond(w http.ResponseWriter, r *http.Request, inc incident.Incident) {
	started := time.Now()
	hs, err := e.investigate(r.Context(), inc)
	if err != nil {
		http.Error(w, "investigation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if e.storage != nil {
		if err := e.storage.Save(r.Context(), storage.Investigation{
			Incident:   inc,
			Hypotheses: hs,
			StartedAt:  started,
			Duration:   time.Since(started),
		}); err != nil {
			log.Printf("storage save: %v", err)
		}
	}
	for _, s := range e.sinks {
		if err := s.Emit(r.Context(), inc, hs); err != nil {
			log.Printf("sink emit: %v", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"incident_id":  inc.ID,
		"hypothesis_n": len(hs),
	})
}

func (e *engine) investigate(ctx context.Context, inc incident.Incident) ([]hypothesis.Hypothesis, error) {
	results := investigator.RunAll(ctx, e.investigators, inc)
	for _, r := range results {
		if r.Err != nil {
			log.Printf("investigator %s: %v", r.Investigator, r.Err)
		}
	}
	ev := investigator.Flatten(results)
	return e.ranker.Rank(ctx, inc, ev)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// openStorage returns the configured Storage backend. With
// LOLO_STORAGE_PATH set, investigations persist to a SQLite file
// across restarts (required for the memory investigator's learning
// pitch). Without it, falls back to the ephemeral ring buffer.
func openStorage() storage.Storage {
	path := os.Getenv("LOLO_STORAGE_PATH")
	if path == "" {
		log.Printf("storage: using ephemeral in-memory ring (set LOLO_STORAGE_PATH to persist)")
		return memory.New(memory.DefaultCapacity)
	}
	s, err := sqlitestore.New(path)
	if err != nil {
		log.Fatalf("storage: sqlite open %q: %v", path, err)
	}
	log.Printf("storage: sqlite at %s", path)
	return s
}

// authMiddleware enforces:
//   - bearer-token auth on /webhook/* (LOLO_WEBHOOK_TOKEN)
//   - basic-auth on /, /investigations/*, /api/* (LOLO_DASHBOARD_TOKEN; password
//     only — the username field is ignored)
//
// /healthz is always open. When a token is empty the corresponding gate is a
// pass-through; main logs a loud warning at startup so this isn't silent.
func authMiddleware(webhookToken, dashboardToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/webhook/") || path == "/investigate":
			if webhookToken == "" {
				break
			}
			if !checkBearer(r, webhookToken) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="lolo"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		case isDashboardPath(path):
			if dashboardToken == "" {
				break
			}
			if !checkBasic(r, dashboardToken) {
				w.Header().Set("WWW-Authenticate", `Basic realm="lolo"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isDashboardPath(p string) bool {
	return p == "/" || strings.HasPrefix(p, "/investigations/") || strings.HasPrefix(p, "/api/")
}

func checkBearer(r *http.Request, token string) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h[len(prefix):]), []byte(token)) == 1
}

func checkBasic(r *http.Request, token string) bool {
	_, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(pass), []byte(token)) == 1
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
