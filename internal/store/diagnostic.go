package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mallexxx/virfield/internal/domain"
)

func diagnosticEvent(kind string) bool {
	return kind == "lease.drift" || kind == "tunnel.listener" || kind == "tunnel.stream" || kind == "backend.observation"
}

// SaveDiagnostic commits an optional state transition, its typed event and
// bounded per-kind retention in one transaction. Existing schema-8 readers can
// still display the JSON message; no new job kind or executable state is added.
func (s *Store) SaveDiagnostic(ctx context.Context, l domain.Lease, d domain.Diagnostic) error {
	if !diagnosticEvent(d.Kind) || d.Version != 1 || len(d.Summary) > 1024 {
		return fmt.Errorf("invalid diagnostic")
	}
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if len(data) > 8192 {
		return fmt.Errorf("diagnostic exceeds safe record bound")
	}
	return s.save(ctx, l, nil, "", "", d.Kind, string(data), nil, &d)
}
