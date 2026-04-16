package investigator

import (
	"context"
	"sort"
	"sync"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

type Investigator interface {
	Name() string
	Investigate(ctx context.Context, inc incident.Incident) ([]evidence.Evidence, error)
}

type Result struct {
	Investigator string
	Evidence     []evidence.Evidence
	Err          error
}

func RunAll(ctx context.Context, inv []Investigator, inc incident.Incident) []Result {
	results := make([]Result, len(inv))
	var wg sync.WaitGroup
	for i, in := range inv {
		wg.Add(1)
		go func(i int, in Investigator) {
			defer wg.Done()
			ev, err := in.Investigate(ctx, inc)
			results[i] = Result{Investigator: in.Name(), Evidence: ev, Err: err}
		}(i, in)
	}
	wg.Wait()
	return results
}

func Flatten(rs []Result) []evidence.Evidence {
	var out []evidence.Evidence
	for _, r := range rs {
		out = append(out, r.Evidence...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
