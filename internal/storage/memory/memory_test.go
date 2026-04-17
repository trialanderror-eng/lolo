package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/incident"
	"github.com/trialanderror-eng/lolo/internal/storage"
)

func mkInv(id string, started time.Time) storage.Investigation {
	return storage.Investigation{
		Incident:  incident.Incident{ID: id, TriggeredAt: started},
		StartedAt: started,
	}
}

func TestSaveAndGet(t *testing.T) {
	s := New(10)
	ctx := context.Background()
	want := mkInv("a", time.Unix(100, 0))
	if err := s.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := s.Get(ctx, "a")
	if err != nil || !ok {
		t.Fatalf("Get(a): ok=%v err=%v", ok, err)
	}
	if got.Incident.ID != "a" {
		t.Errorf("got %+v, want id=a", got)
	}
	if _, ok, _ := s.Get(ctx, "missing"); ok {
		t.Error("Get(missing) returned ok=true")
	}
}

func TestList_newestFirstAndLimit(t *testing.T) {
	s := New(10)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.Save(ctx, mkInv(fmt.Sprintf("inv-%d", i), time.Unix(int64(i*100), 0))); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	got, err := s.List(ctx, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	wantIDs := []string{"inv-4", "inv-3", "inv-2"}
	for i, w := range wantIDs {
		if got[i].Incident.ID != w {
			t.Errorf("got[%d].ID = %q, want %q", i, got[i].Incident.ID, w)
		}
	}
}

func TestRingEvictsOldestWhenFull(t *testing.T) {
	s := New(3)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = s.Save(ctx, mkInv(fmt.Sprintf("inv-%d", i), time.Unix(int64(i), 0)))
	}
	// After 5 saves with capacity 3, oldest two (inv-0, inv-1) should be gone.
	for _, id := range []string{"inv-0", "inv-1"} {
		if _, ok, _ := s.Get(ctx, id); ok {
			t.Errorf("Get(%s): want ok=false (evicted), got ok=true", id)
		}
	}
	for _, id := range []string{"inv-2", "inv-3", "inv-4"} {
		if _, ok, _ := s.Get(ctx, id); !ok {
			t.Errorf("Get(%s): want ok=true (retained), got ok=false", id)
		}
	}
	all, _ := s.List(ctx, 0)
	if len(all) != 3 {
		t.Errorf("List size = %d, want 3 (capacity)", len(all))
	}
}

func TestEmptyStorage(t *testing.T) {
	s := New(5)
	ctx := context.Background()
	got, err := s.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

func TestConcurrentSaveAndList(t *testing.T) {
	s := New(100)
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			_ = s.Save(ctx, mkInv(fmt.Sprintf("c-%d", i), time.Unix(int64(i), 0)))
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
