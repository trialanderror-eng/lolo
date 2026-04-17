package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
	"github.com/trialanderror-eng/lolo/internal/storage"
)

func openTemp(t *testing.T) (*Storage, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lolo.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func mkInv(id, summary string) storage.Investigation {
	return storage.Investigation{
		StartedAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
		Duration:  150 * time.Millisecond,
		Incident: incident.Incident{
			ID:          id,
			TriggeredAt: time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
			Scope:       incident.Scope{Services: []string{"payments"}},
			Signal:      incident.Signal{Source: "alertmanager", Summary: summary},
		},
		Hypotheses: []hypothesis.Hypothesis{{
			Summary: "top",
			Score:   0.9,
			Evidence: []evidence.Evidence{{
				Source: "kubernetes", Kind: "pod_unhealthy",
				Summary: "broken",
				Links:   []evidence.Link{{Label: "graph", URL: "http://p/graph"}},
			}},
		}},
	}
}

func TestSaveAndGet(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	want := mkInv("a", "sig")
	if err := s.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := s.Get(ctx, "a")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Incident.ID != "a" {
		t.Errorf("got %+v", got)
	}
	if len(got.Hypotheses) != 1 || len(got.Hypotheses[0].Evidence) != 1 {
		t.Errorf("nested structure lost on roundtrip: %+v", got)
	}
	if got.Hypotheses[0].Evidence[0].Links[0].URL != "http://p/graph" {
		t.Errorf("link lost: %+v", got.Hypotheses[0].Evidence[0])
	}
}

func TestGet_notFound(t *testing.T) {
	s, _ := openTemp(t)
	_, ok, err := s.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Get(missing): err = %v, want nil", err)
	}
	if ok {
		t.Error("ok = true for missing id")
	}
}

func TestSave_upsertsSameID(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	first := mkInv("x", "v1")
	_ = s.Save(ctx, first)
	second := mkInv("x", "v2")
	_ = s.Save(ctx, second)

	got, _, _ := s.Get(ctx, "x")
	if got.Incident.Signal.Summary != "v2" {
		t.Errorf("summary = %q, want v2 (upsert)", got.Incident.Signal.Summary)
	}
	all, _ := s.List(ctx, 0)
	if len(all) != 1 {
		t.Errorf("List len = %d, want 1 (upsert shouldn't duplicate)", len(all))
	}
}

func TestList_newestFirstAndLimit(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		inv := mkInv(string(rune('a'+i)), "sig")
		inv.StartedAt = time.Unix(int64(100*i), 0)
		_ = s.Save(ctx, inv)
	}
	got, err := s.List(ctx, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantOrder := []string{"e", "d", "c"}
	for i, w := range wantOrder {
		if got[i].Incident.ID != w {
			t.Errorf("[%d] = %q, want %q (newest first)", i, got[i].Incident.ID, w)
		}
	}
}

func TestPersistence_survivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lolo.db")

	s1, err := New(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s1.Save(context.Background(), mkInv("persist-me", "before restart")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	got, ok, err := s2.Get(context.Background(), "persist-me")
	if err != nil || !ok {
		t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
	}
	if got.Incident.Signal.Summary != "before restart" {
		t.Errorf("data lost across reopen: %+v", got)
	}
}

func TestConcurrentSaveAndList(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_ = s.Save(ctx, mkInv(string(rune('a'+i%26))+"-"+string(rune('0'+i/26)), "sig"))
		}
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		default:
			_, _ = s.List(ctx, 10)
		}
	}
}
