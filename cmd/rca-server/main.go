package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
	"github.com/trialanderror-eng/lolo/internal/investigator"
	"github.com/trialanderror-eng/lolo/internal/investigators/stub"
	"github.com/trialanderror-eng/lolo/internal/output/stdout"
)

func main() {
	id := flag.String("id", "demo-incident", "incident id for the dry-run")
	flag.Parse()

	inc := incident.Incident{
		ID:          *id,
		TriggeredAt: time.Now(),
		Window:      30 * time.Minute,
		Signal:      incident.Signal{Source: "manual", Summary: "dry-run"},
	}

	invs := []investigator.Investigator{stub.New()}
	results := investigator.RunAll(context.Background(), invs, inc)
	ev := investigator.Flatten(results)

	hs := []hypothesis.Hypothesis{{
		Summary:   "no ranker configured yet",
		Score:     0,
		Evidence:  ev,
		Reasoning: "wiring placeholder — plug in a real Ranker to synthesize hypotheses",
	}}

	if err := stdout.New().Emit(context.Background(), inc, hs); err != nil {
		log.Fatal(err)
	}
}
