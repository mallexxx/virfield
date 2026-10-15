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
// bounded per-kind retention in one transaction.
func (s *Store) SaveDiagnostic(ctx context.Context, l domain.Lease, d domain.Diagnostic) error {
	return s.SaveJobDiagnostic(ctx, l, nil, "", "", d)
}

func (s *Store) SaveJobDiagnostic(ctx context.Context, l domain.Lease, j *domain.Job, key, fp string, d domain.Diagnostic) error {
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
	return s.save(ctx, l, j, key, fp, d.Kind, string(data), nil, &d)
}
