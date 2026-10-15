package store

import (
	"context"
	"fmt"
	"github.com/mallexxx/virfield/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestDiagnosticBoundsRestartAndCleanupPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l := domain.Lease{ID: "lease-diagnostic", State: "ready", UpdatedAt: time.Now().UTC()}
	for _, kind := range []string{"lease.drift", "tunnel.listener", "tunnel.stream", "backend.observation"} {
		for i := 0; i < 40; i++ {
			d := domain.Diagnostic{Version: 1, Kind: kind, Action: "test", Reason: "test", Summary: fmt.Sprintf("%s/%d", kind, i), At: time.Now().UTC()}
			if err := s.SaveDiagnostic(ctx, l, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	l.State = "released"
	if err := s.Save(ctx, l, nil, "", "", "cleanup.done", "Owned fixture released"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(ctx, 0, l.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		if event.Diagnostic != nil {
			counts[event.Type]++
			if event.Message != event.Diagnostic.Summary || event.Diagnostic.Version != 1 {
				t.Fatal(event)
			}
		}
	}
	if len(events) != 129 || len(counts) != 4 {
		t.Fatal(len(events), counts)
	}
	for kind, count := range counts {
		if count != 32 {
			t.Fatal(kind, count)
		}
	}
	stored, err := s.Lease(ctx, l.ID)
	if err != nil || stored.State != "released" || !stored.UpdatedAt.Equal(l.UpdatedAt) {
		t.Fatal(stored, err)
	}
}
