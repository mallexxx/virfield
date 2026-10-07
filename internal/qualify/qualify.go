// Package qualify holds runtime-agnostic guest qualification checks. Every
// check runs a fixed command over an authenticated, host-key-pinned session.
package qualify

import (
	"context"
	"io"
	"strings"
	"time"
)

// Guest is the authenticated SSH session a check runs on.
type Guest interface {
	Run(ctx context.Context, command, input string) (string, error)
	RunReader(ctx context.Context, timeout time.Duration, command string, input io.Reader) (string, error)
}

// Quote returns a single-quoted shell word.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
