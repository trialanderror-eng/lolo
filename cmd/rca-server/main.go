package main

import (
	"context"
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
	"github.com/trialanderror-eng/lolo/internal/investigators/stub"
	"github.com/trialanderror-eng/lolo/internal/output/slack"
	"github.com/trialanderror-eng/lolo/internal/output/stdout"
	"github.com/trialanderror-eng/lolo/internal/trigger/alertmanager"
)

func main() {
	addr := flag.String("addr", envOr("LOLO_ADDR", ":8080"), "listen address")
	flag.Parse()

	invs := []investigator.Investigator{stub.New()}
	if token := os.Getenv("LOLO_GITHUB_TOKEN"); token != "" {
		invs = append(invs, deploys.New(token, splitCSV(os.Getenv("LOLO_GITHUB_REPOS"))))
	}
	invs = append(invs, k8sinv.New(splitCSV(os.Getenv("LOLO_K8S_NAMESPACES"))))

	sinks := []Sink{stdout.New()}
	if url := os.Getenv("LOLO_SLACK_WEBHOOK_URL"); url != "" {
		sinks = append(sinks, slack.New(url))
	}

	engine := &engine{
		investigators: invs,
		ranker:        hypothesis.DefaultRanker{},
		sinks:         sinks,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/webhook/alertmanager", engine.handleAlertmanager)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
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
	hs, err := e.investigate(r.Context(), inc)
	if err != nil {
		http.Error(w, "investigation failed: "+err.Error(), http.StatusInternalServerError)
		return
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
