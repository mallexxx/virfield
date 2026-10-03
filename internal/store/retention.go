package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type PruneResult struct {
	Leases   int64
	Jobs     int64
	Requests int64
	Events   int64
}

// Prune removes completed records older than the supplied UTC cutoff. Active
// lease records, their jobs and idempotency keys are never removed. Every
// deletion commits together, so a failure cannot leave dangling references.
func (s *Store) Prune(ctx context.Context, before time.Time) (PruneResult, error) {
	var result PruneResult
	if before.IsZero() {
		return result, fmt.Errorf("retention cutoff is required")
	}
	cutoff := before.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,body FROM leases WHERE state='released'`)
	if err != nil {
		return result, err
	}
	var candidates []string
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			rows.Close()
			return result, err
		}
		var lease domain.Lease
		if err := json.Unmarshal([]byte(body), &lease); err != nil {
			rows.Close()
			return result, err
		}
		if !lease.UpdatedAt.IsZero() && lease.UpdatedAt.Before(cutoff) {
			candidates = append(candidates, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	remove := func(query string, args ...any) (int64, error) {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	for _, id := range candidates {
		var unfinished int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE lease_id=? AND state IN ('queued','running','needs_attention')`, id).Scan(&unfinished); err != nil {
			return PruneResult{}, err
		}
		if unfinished != 0 {
			continue
		}
		if n, err := remove(`DELETE FROM requests WHERE lease_id=?`, id); err != nil {
			return PruneResult{}, err
		} else {
			result.Requests += n
		}
		if n, err := remove(`DELETE FROM events WHERE lease_id=?`, id); err != nil {
			return PruneResult{}, err
		} else {
			result.Events += n
		}
		if n, err := remove(`DELETE FROM jobs WHERE lease_id=?`, id); err != nil {
			return PruneResult{}, err
		} else {
			result.Jobs += n
		}
		if n, err := remove(`DELETE FROM leases WHERE id=? AND state='released'`, id); err != nil {
			return PruneResult{}, err
		} else {
			result.Leases += n
		}
	}
	// Stored UTC timestamps use RFC3339Nano with optional fractional seconds.
	// Compare the whole-second prefix so the index remains usable. Events in
	// the cutoff second get at most one extra second of retention.
	cutoffText := cutoff.Format("2006-01-02T15:04:05")
	if n, err := remove(`DELETE FROM events WHERE at<?`, cutoffText); err != nil {
		return PruneResult{}, err
	} else {
		result.Events += n
	}
	if err := tx.Commit(); err != nil {
		return PruneResult{}, err
	}
	return result, nil
}
