package control

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/store"
)

func TestMatchingPersistedAndConfiguredTemplateDoesNotBlockStartup(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	template := domain.Template{ID: "pinned", Name: "golden", Location: "home"}
	lease := domain.Lease{ID: "image-persisted", Purpose: "image", Template: template.ID, VMName: template.Name, Location: template.Location, State: "released"}
	if err := s.SaveImage(context.Background(), lease, nil, "", "", "image.recorded", "fixture", template); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := New(s, newBackend(), []domain.Template{template}, 2, log); err != nil {
		t.Fatalf("identical pinned definition prevented startup: %v", err)
	}
	legacy := template
	legacy.LegacyUUID = "123e4567-e89b-12d3-a456-426614174000"
	withLegacy, err := New(s, newBackend(), []domain.Template{legacy}, 2, log)
	if err != nil {
		t.Fatalf("legacy UUID override prevented startup: %v", err)
	}
	if withLegacy.templates[legacy.ID].LegacyUUID != legacy.LegacyUUID {
		t.Fatalf("legacy UUID override was not retained: %#v", withLegacy.templates[legacy.ID])
	}
	changed := template
	changed.Name = "different-golden"
	if _, err := New(s, newBackend(), []domain.Template{changed}, 2, log); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting persisted definition was silently adopted: %v", err)
	}
}
