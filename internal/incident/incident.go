package incident

import (
	"time"
)

type Incident struct {
	ID          string
	TriggeredAt time.Time
	Window      time.Duration
	Scope       Scope
	Signal      Signal
}

type Scope struct {
	Services   []string
	Namespaces []string
	Clusters   []string
	Repos      []string
}

type Signal struct {
	Source  string
	Summary string
	Raw     map[string]any
}

func (i Incident) Start() time.Time { return i.TriggeredAt.Add(-i.Window) }
func (i Incident) End() time.Time   { return i.TriggeredAt }
