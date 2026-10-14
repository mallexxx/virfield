package control

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/fleet"
	"github.com/mallexxx/virfield/internal/store"
)

// nodeBackend is a physical node runtime: one host with one lease slot and a
// pseudo-golden named after its booted volume.
type nodeBackend struct {
	*fakeBackend
	maxTargets int
}

func (b *nodeBackend) ProviderName() string { return "baremetal" }

func (b *nodeBackend) Observe(ctx context.Context) (domain.Observation, error) {
	o, err := b.fakeBackend.Observe(ctx)
	if err != nil {
		return o, err
	}
	o.HostMax = b.maxTargets
	if o.HostMax == 0 {
		o.HostMax = 1
	}
	o.Hosts = []domain.Host{{ID: "mbp", Provider: "baremetal", Availability: "available", Capacity: domain.HostCapacity{MaxVMs: o.HostMax, UsedVMs: o.HostUsed}}}
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
	node := &nodeBackend{fakeBackend: &fakeBackend{vms: map[string]domain.VM{"mbp/Monterey": {Name: "Monterey", Location: "mbp", OS: "macOS", State: "stopped"}}, counts: map[string]int{}}}
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

// A node lease is bounded by the node's own slot, never by this Mac's CPU,
// memory or clone-storage budget.
func TestNodeLeaseIsNotChargedToMacResources(t *testing.T) {
	c, vms, node := nodeController(t)
	vms.mu.Lock()
	macGolden := vms.vms["home/golden"]
	macGolden.Resources = domain.Resources{CPU: 1, MemoryBytes: 1 << 30, DiskBytes: 1 << 30}
	vms.vms["home/golden"] = macGolden
	vms.mu.Unlock()
	node.mu.Lock()
	golden := node.vms["mbp/Monterey"]
	golden.Resources = domain.Resources{CPU: 4, MemoryBytes: 16 << 30}
	node.vms["mbp/Monterey"] = golden
	node.mu.Unlock()
	c.SetResources(domain.ResourceLimits{CPU: 2, MemoryBytes: 4 << 30, DiskReserveBytes: 1}, func() (map[string]int64, error) {
		return map[string]int64{"home": 1 << 40}, nil
	})
	tick(t, c)
	op, err := c.Acquire(context.Background(), "node-resources", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600})
	if err != nil {
		t.Fatal("node lease charged to Mac resources:", err)
	}
	if op.Lease.Resources != (domain.Resources{}) {
		t.Fatalf("node lease reserved Mac resources: %#v", op.Lease.Resources)
	}
	// Mac VM admission keeps working while a node lease is active.
	if _, err := c.Acquire(context.Background(), "vm-after-node", domain.AcquireRequest{Template: "test", TTLSeconds: 3600}); err != nil {
		t.Fatal("Mac VM admission blocked by an active node lease:", err)
	}
}

func nodeImageController(t *testing.T) (*Controller, *fakeImageBuilder) {
	t.Helper()
	c, _, node := nodeController(t)
	node.maxTargets = 3
	tick(t, c)
	p := domain.ImageProfile{Node: &domain.NodeSource{Golden: "sonoma-bootstrap"}, MacOS: "14.8.9", Build: "23J631", DisableSIP: true}
	c.templates["mbp-sonoma"] = domain.Template{ID: "mbp-sonoma", Name: "mbp-sonoma", Location: "mbp", Provider: "baremetal", OS: "macOS", GuestUser: "admin", GuestHome: "/Users/admin", Image: &p}
	builder := &fakeImageBuilder{backend: node.fakeBackend}
	c.SetImageBuilder(builder)
	return c, builder
}

func TestNodeImageRunsBuildQualifyStopCommit(t *testing.T) {
	c, b := nodeImageController(t)
	ctx := context.Background()
	// Full Mac VM capacity must not block an image built on a node.
	for _, key := range []string{"vm-one-lease", "vm-two-lease"} {
		if _, err := c.Acquire(ctx, key, domain.AcquireRequest{Template: "test", TTLSeconds: 3600}); err != nil {
			t.Fatal(err)
		}
	}
	op, err := c.BuildImage(ctx, "mbp-sonoma", "node-image-build")
	if err != nil {
		t.Fatal(err)
	}
	if op.Lease.Provider != "baremetal" || op.Lease.Location != "mbp" {
		t.Fatal(op.Lease.Provider, op.Lease.Location)
	}
	for range 12 {
		tick(t, c)
	}
	if strings.Join(b.steps, ",") != "build,qualify,stop,commit" {
		t.Fatal(b.steps)
	}
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "image_ready" || l.ImageManifest != imageFingerprint(c.templates["mbp-sonoma"].Image) {
		t.Fatal(l.State, err)
	}
}

func TestNodeImageQualifyRetriesButCommitDoesNot(t *testing.T) {
	c, b := nodeImageController(t)
	ctx := context.Background()
	b.fail = "qualify"
	op, err := c.BuildImage(ctx, "mbp-sonoma", "node-image-qualify")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		tick(t, c)
	}
	b.fail = "commit"
	tick(t, c)
	if _, err := c.RecoverImage(ctx, op.Lease.ID, "node-retry-qualify", "mbp-sonoma", "retry", true); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		tick(t, c)
	}
	j, err := c.Job(ctx, op.Job.ID)
	if err != nil || j.Phase != "commit_dispatched" || j.State != "needs_attention" {
		t.Fatal(j.Phase, j.State, err)
	}
	_, err = c.RecoverImage(ctx, op.Lease.ID, "node-retry-commit", "mbp-sonoma", "retry", true)
	code(t, err, "unsafe_retry")
}

func TestNodeHostRemainsExclusiveWithVMSlots(t *testing.T) {
	c, _, node := nodeController(t)
	node.maxTargets = 3
	tick(t, c)
	ctx := context.Background()
	if _, err := c.AcquireAs(ctx, "exclusive-host-one", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600, SSHPublicKey: testPublicKey(t)}, "one"); err != nil {
		t.Fatal(err)
	}
	_, err := c.AcquireAs(ctx, "exclusive-host-two", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600, SSHPublicKey: testPublicKey(t)}, "two")
	code(t, err, "capacity_exhausted")
}
func TestNodeImageDeletionKeepsOwningProvider(t *testing.T) {
	c, _ := nodeImageController(t)
	ctx := context.Background()
	if _, err := c.BuildImage(ctx, "mbp-sonoma", "node-delete-build"); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		tick(t, c)
	}
	op, err := c.DeleteImage(ctx, "mbp-sonoma", "node-delete-golden", "mbp-sonoma")
	if err != nil {
		t.Fatal(err)
	}
	if op.Lease.Provider != "baremetal" || op.Lease.GuestUser != "admin" {
		t.Fatalf("lost image identity: %#v", op.Lease)
	}
	for range 4 {
		tick(t, c)
	}
	j, err := c.Job(ctx, op.Job.ID)
	if err != nil || j.State != "succeeded" {
		t.Fatal(j, err)
	}
}
func TestPhysicalHostCannotBeDeletedAsImage(t *testing.T) {
	c, _, _ := nodeController(t)
	_, err := c.DeleteImage(context.Background(), "mbp-monterey", "delete-host-refused", "Monterey")
	code(t, err, "invalid_request")
}
