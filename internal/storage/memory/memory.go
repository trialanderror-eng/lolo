// Package memory is a Storage backed by a fixed-capacity ring buffer.
// Lost on restart by design — this is the v0.2.0 default; persistent
// backends land later behind the same interface.
package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/trialanderror-eng/lolo/internal/storage"
)

const DefaultCapacity = 1000

type Storage struct {
	mu      sync.RWMutex
	entries []storage.Investigation
	byID    map[string]int // id → index in entries
	cap     int
	next    int // ring write head
}

func New(capacity int) *Storage {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Storage{
		entries: make([]storage.Investigation, 0, capacity),
		byID:    make(map[string]int, capacity),
		cap:     capacity,
	}
}

func (s *Storage) Save(_ context.Context, inv storage.Investigation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) < s.cap {
		s.byID[inv.Incident.ID] = len(s.entries)
		s.entries = append(s.entries, inv)
		return nil
	}
	// Buffer is full — evict the slot at s.next (oldest by insertion order)
	// and reuse it. This is a simple ring; not strictly oldest-by-StartedAt,
	// but for a steady-state RCA stream the two are the same.
	old := s.entries[s.next]
	if idx, ok := s.byID[old.Incident.ID]; ok && idx == s.next {
		delete(s.byID, old.Incident.ID)
	}
	s.entries[s.next] = inv
	s.byID[inv.Incident.ID] = s.next
	s.next = (s.next + 1) % s.cap
	return nil
}

func (s *Storage) Get(_ context.Context, id string) (storage.Investigation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, ok := s.byID[id]
	if !ok {
		return storage.Investigation{}, false, nil
	}
	return s.entries[idx], true, nil
}

// List returns up to limit investigations, newest first by StartedAt.
// limit <= 0 means unlimited.
func (s *Storage) List(_ context.Context, limit int) ([]storage.Investigation, error) {
	s.mu.RLock()
	out := make([]storage.Investigation, len(s.entries))
	copy(out, s.entries)
	s.mu.RUnlock()

	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
