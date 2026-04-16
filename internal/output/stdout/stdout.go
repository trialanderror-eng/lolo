package stdout

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

type Sink struct {
	w io.Writer
}

func New() *Sink { return &Sink{w: os.Stdout} }

func (s *Sink) Emit(_ context.Context, inc incident.Incident, hs []hypothesis.Hypothesis) error {
	enc := json.NewEncoder(s.w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"incident":   inc,
		"hypotheses": hs,
	})
}
