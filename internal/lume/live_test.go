package lume

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveReadOnly is opt-in and never invokes mutation endpoints.
func TestLiveReadOnly(t *testing.T) {
	base := os.Getenv("VIRFIELD_LIVE_LUME_URL")
	if base == "" {
		t.Skip("set VIRFIELD_LIVE_LUME_URL for read-only installed Lume contract check")
	}
	c, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o, err := c.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Lume reports %d/%d slots; %d VMs", o.HostUsed, o.HostMax, len(o.VMs))
}
