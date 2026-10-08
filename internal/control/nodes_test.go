package control

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/fleet"
	"github.com/mallexxx/virfield/internal/store"
)

// nodeBackend is a physical node runtime: one host with one lease slot and a
// pseudo-golden named after its booted volume.
type nodeBackend struct{ *fakeBackend }

func (b *nodeBackend) ProviderName() string { return "baremetal" }

func (b *nodeBackend) Observe(ctx context.Context) (domain.Observation, error) {
	o, err := b.fakeBackend.Observe(ctx)
	if err != nil {
		return o, err
	}
	o.HostMax = 1
	o.Hosts = []domain.Host{{ID: "mbp", Provider: "baremetal", Availability: "available", Capacity: domain.HostCapacity{MaxVMs: 1, UsedVMs: o.HostUsed}}}
	o.StorageContainers = []domain.StorageContainer{{ID: "mbp", HostID: "mbp", Provider: "baremetal", Availability: "available"}}
	return o, nil
}

func nodeController(t *testing.T) (*Controller, *fakeBackend, *nodeBackend) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	vms := newBackend()
	node := &nodeBackend{&fakeBackend{vms: map[string]domain.VM{"mbp/Monterey": {Name: "Monterey", Location: "mbp", OS: "macOS", State: "stopped"}}, counts: map[string]int{}}}
	ts := []domain.Template{
		{ID: "test", Name: "golden", Location: "home"},
		{ID: "mbp-monterey", Name: "Monterey", Location: "mbp", Provider: "baremetal", OS: "macOS", GuestUser: "admin"},
	}
	c, err := New(s, fleet.New(vms, node), ts, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	return c, vms, node
}

func TestNodeLeaseDoesNotConsumeMacVMSlots(t *testing.T) {
	c, _, _ := nodeController(t)
	ctx := context.Background()
	for _, key := range []string{"vm-one-lease", "vm-two-lease"} {
		if _, err := c.Acquire(ctx, key, domain.AcquireRequest{Template: "test", TTLSeconds: 3600}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Acquire(ctx, "node-one-lease", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600}); err != nil {
		t.Fatal("node lease blocked by VM slots:", err)
	}
	_, err := c.Acquire(ctx, "node-two-lease", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600})
	code(t, err, "capacity_exhausted")
	status, err := c.Status(ctx)
	if err != nil || status.Capacity.Limit != 2 || status.Capacity.Used != 2 {
		t.Fatalf("Mac capacity = %#v %v", status.Capacity, err)
	}
}

func TestNodeLeaseRunsOnItsNodeWithItsProvider(t *testing.T) {
	c, vms, node := nodeController(t)
	c.defaultCloneLocation = "home"
	ctx := context.Background()
	op, err := c.Acquire(ctx, "node-lease", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if op.Lease.Location != "mbp" || op.Lease.Provider != "baremetal" || op.Lease.GuestUser != "admin" {
		t.Fatalf("lease = %#v", op.Lease)
	}
	for range 4 {
		tick(t, c)
	}
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "ready" {
		t.Fatal(l.State, err)
	}
	if node.count("clone") != 1 || node.count("start") != 1 || vms.count("clone") != 0 {
		t.Fatal("lease ran on the wrong runtime")
	}
}

func TestNodeTemplatesNeedAClientKeyButNoImageProfile(t *testing.T) {
	c, _, _ := nodeController(t)
	p := &blockingPreparer{started: make(chan domain.Lease, 1), finish: make(chan struct{})}
	c.SetLeasePreparer(p)
	ctx := context.Background()
	_, err := c.Acquire(ctx, "node-without-key", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600})
	code(t, err, "ssh_profile_missing")
	_, err = c.Acquire(ctx, "vm-without-image", domain.AcquireRequest{Template: "test", TTLSeconds: 3600, SSHPublicKey: testPublicKey(t)})
	code(t, err, "ssh_profile_missing")
	if _, err := c.Acquire(ctx, "node-with-key", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600, SSHPublicKey: testPublicKey(t)}); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	tick(t, c)
	// tick waits for workers; dispatch SSH preparation without waiting.
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case l := <-p.started:
		if l.Provider != "baremetal" {
			t.Fatal(l.Provider)
		}
	case <-time.After(time.Second):
		t.Fatal("node lease never reached SSH preparation")
	}
	close(p.finish)
	c.wg.Wait()
}
