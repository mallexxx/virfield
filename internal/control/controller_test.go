package control

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/lume"
	"github.com/mallexxx/virfield/internal/store"
)

type fakeBackend struct {
	mu           sync.Mutex
	vms          map[string]domain.VM
	counts       map[string]int
	err          error
	cloneErr     error
	startErr     error
	stopErr      error
	deleteErr    error
	delayedStart bool
	blockClone   chan struct{}
	hostExtra    int
}

func newBackend() *fakeBackend {
	return &fakeBackend{vms: map[string]domain.VM{"home/golden": {Name: "golden", Location: "home", OS: "macOS", State: "stopped"}}, counts: map[string]int{}}
}
func (b *fakeBackend) Observe(context.Context) (domain.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return domain.Observation{}, b.err
	}
	o := domain.Observation{VMs: []domain.VM{}, HostMax: 2, HostUsed: b.hostExtra, At: time.Now().UTC()}
	for _, v := range b.vms {
		o.VMs = append(o.VMs, v)
		if v.State != "stopped" {
			o.HostUsed++
		}
	}
	return o, nil
}
func (b *fakeBackend) Clone(ctx context.Context, _ domain.Template, l domain.Lease) error {
	b.mu.Lock()
	b.counts["clone"]++
	ch := b.blockClone
	b.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.vms[l.Key()] = domain.VM{Name: l.VMName, Location: l.Location, OS: "macOS", State: "stopped"}
	return b.cloneErr
}
func (b *fakeBackend) Start(_ context.Context, l domain.Lease) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.counts["start"]++
	if !b.delayedStart {
		v := b.vms[l.Key()]
		v.State = "running"
		v.IP = "192.168.64.10"
		v.SSHAvailable = true
		b.vms[l.Key()] = v
	}
	return b.startErr
}
func (b *fakeBackend) Stop(_ context.Context, l domain.Lease) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.counts["stop"]++
	if b.stopErr != nil {
		return b.stopErr
	}
	v := b.vms[l.Key()]
	v.State = "stopped"
	b.vms[l.Key()] = v
	return nil
}
func (b *fakeBackend) Delete(_ context.Context, l domain.Lease) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.counts["delete"]++
	if b.deleteErr != nil {
		return b.deleteErr
	}
	delete(b.vms, l.Key())
	return nil
}
func (b *fakeBackend) count(op string) int { b.mu.Lock(); defer b.mu.Unlock(); return b.counts[op] }

var templates = []domain.Template{{ID: "test", Name: "golden", Location: "home"}}

func setup(t *testing.T) (*Controller, *store.Store, *fakeBackend) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b := newBackend()
	c, err := New(s, b, templates, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	return c, s, b
}
func tick(t *testing.T, c *Controller) {
	t.Helper()
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.wg.Wait()
}
func acquire(t *testing.T, c *Controller, key string) domain.Operation {
	t.Helper()
	op, err := c.Acquire(context.Background(), key, domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("error=%v; want %s", err, want)
	}
}

func TestLegacyUUIDAllowsOnlyOneActiveWorker(t *testing.T) {
	c, _, b := setup(t)
	legacyUUID := "123e4567-e89b-12d3-a456-426614174000"
	c.templates["test"] = domain.Template{ID: "test", Name: "golden", Location: "home", LegacyUUID: legacyUUID}
	c.templates["legacy-copy"] = domain.Template{ID: "legacy-copy", Name: "other-golden", Location: "home", LegacyUUID: legacyUUID}
	b.vms["home/other-golden"] = domain.VM{Name: "other-golden", Location: "home", OS: "macOS", State: "stopped"}

	first := acquire(t, c, "legacy-first")
	if first.Lease.LegacyUUID != legacyUUID {
		t.Fatalf("lease lost legacy UUID: %#v", first.Lease)
	}
	_, err := c.Acquire(context.Background(), "legacy-second", domain.AcquireRequest{Template: "legacy-copy", TTLSeconds: 3600})
	code(t, err, "legacy_uuid_in_use")

	if _, err := c.Release(context.Background(), first.Lease.ID, "legacy-release"); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	if _, err := c.Acquire(context.Background(), "legacy-after-release", domain.AcquireRequest{Template: "legacy-copy", TTLSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyUUIDMustBeValid(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_, err = New(s, newBackend(), []domain.Template{{ID: "test", Name: "golden", Location: "home", LegacyUUID: "not-a-uuid"}}, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("invalid legacy UUID accepted")
	}
}
func ready(t *testing.T, c *Controller, op domain.Operation) {
	t.Helper()
	for range 3 {
		tick(t, c)
	}
	l, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || l.State != "ready" {
		t.Fatalf("lease=%+v error=%v", l, err)
	}
}

func TestConcurrentAdmissionAndReplay(t *testing.T) {
	c, _, b := setup(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	denied := 0
	for i := range 30 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := c.Acquire(context.Background(), domain.NewID("request-"), domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				accepted++
			} else {
				var e *domain.Error
				if !errors.As(err, &e) || e.Code != "capacity_exhausted" {
					t.Errorf("%v", err)
				}
				denied++
			}
		}(i)
	}
	wg.Wait()
	if accepted != 2 || denied != 28 {
		t.Fatalf("accepted=%d denied=%d", accepted, denied)
	}
	if b.count("clone") != 0 {
		t.Fatal("acceptance must not run side effects")
	}
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Capacity.Used != 2 {
		t.Fatal(status.Capacity)
	}
}
func TestIdempotencyConflictAndReplayAfterRelease(t *testing.T) {
	c, _, b := setup(t)
	op := acquire(t, c, "request-1")
	again := acquire(t, c, "request-1")
	if !again.Replayed || again.Lease.ID != op.Lease.ID {
		t.Fatal("not replayed")
	}
	_, err := c.Acquire(context.Background(), "request-1", domain.AcquireRequest{Template: "test", TTLSeconds: 7200})
	code(t, err, "idempotency_conflict")
	ready(t, c, op)
	cleanup, err := c.Release(context.Background(), op.Lease.ID, "release-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Release(context.Background(), op.Lease.ID, "release-2")
	if err != nil || !second.Replayed || second.Job.ID != cleanup.Job.ID {
		t.Fatalf("second release did not converge on cleanup: %+v %v", second, err)
	}
	for range 3 {
		tick(t, c)
	}
	l, _ := c.Lease(context.Background(), op.Lease.ID)
	if l.State != "released" {
		t.Fatal(l)
	}
	replay, err := c.Release(context.Background(), op.Lease.ID, "release-1")
	if err != nil || !replay.Replayed || replay.Job.ID != cleanup.Job.ID {
		t.Fatalf("%+v %v", replay, err)
	}
	released, err := c.Release(context.Background(), op.Lease.ID, "release-3")
	if err != nil || released.Lease.State != "released" || released.Job.ID != cleanup.Job.ID {
		t.Fatalf("release after expiry must return completed cleanup: %+v %v", released, err)
	}
	again = acquire(t, c, "request-1")
	if again.Lease.State != "released" {
		t.Fatal("replay created a new lease")
	}
	if b.count("clone") != 1 || b.count("start") != 1 || b.count("stop") != 1 || b.count("delete") != 1 {
		t.Fatal(b.counts)
	}
}
func TestCapacityCountsExternalAndUnknownSlots(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "inventory", true: "host-only"}[unknown], func(t *testing.T) {
			c, _, b := setup(t)
			b.mu.Lock()
			if unknown {
				b.hostExtra = 2
			} else {
				b.vms["home/external-a"] = domain.VM{Name: "external-a", Location: "home", State: "running"}
				b.vms["home/external-b"] = domain.VM{Name: "external-b", Location: "home", State: "running"}
			}
			b.mu.Unlock()
			tick(t, c)
			_, err := c.Acquire(context.Background(), "request-1", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
			code(t, err, "capacity_exhausted")
			if b.count("stop") != 0 || b.count("delete") != 0 {
				t.Fatal("external VM touched")
			}
		})
	}
}
func TestUnavailableAndStaleInventoryFailClosed(t *testing.T) {
	c, _, b := setup(t)
	b.err = errors.New("timeout")
	tick(t, c)
	_, err := c.Acquire(context.Background(), "request-1", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "backend_unavailable")
	b.err = nil
	tick(t, c)
	c.lastObservedMono = time.Now().Add(-20 * time.Second)
	_, err = c.Acquire(context.Background(), "request-2", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "backend_unavailable")
}
func TestCloneAmbiguityQuarantinesWithoutReplayOrDelete(t *testing.T) {
	c, _, b := setup(t)
	b.cloneErr = errors.New("lost response")
	op := acquire(t, c, "request-1")
	tick(t, c)
	for range 3 {
		tick(t, c)
	}
	l, _ := c.Lease(context.Background(), op.Lease.ID)
	if l.State != "quarantined" {
		t.Fatal(l)
	}
	_, err := c.Release(context.Background(), l.ID, "release-1")
	code(t, err, "outcome_unknown")
	if b.count("clone") != 1 || b.count("delete") != 0 || b.count("start") != 0 {
		t.Fatal(b.counts)
	}
}
func TestRestartDuringCloneDoesNotAdoptPartialDisk(t *testing.T) {
	c, s, b := setup(t)
	op := acquire(t, c, "request-1")
	l, j := op.Lease, op.Job
	l.State = "provisioning"
	j.State = "running"
	j.Phase = "clone_dispatched"
	if err := s.Save(context.Background(), l, &j, "", "", "job.dispatched", "clone_dispatched"); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(s, b, templates, 2, c.log)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	tick(t, recovered)
	got, _ := s.Lease(context.Background(), l.ID)
	if got.State != "quarantined" {
		t.Fatal(got)
	}
	if b.count("clone") != 0 {
		t.Fatal("clone replayed")
	}
}
func TestRestartAfterStartObservesWithoutReplay(t *testing.T) {
	c, s, b := setup(t)
	op := acquire(t, c, "request-1")
	tick(t, c)
	tick(t, c)
	recovered, err := New(s, b, templates, 2, c.log)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	tick(t, recovered)
	l, _ := s.Lease(context.Background(), op.Lease.ID)
	if l.State != "ready" {
		t.Fatal(l)
	}
	if b.count("start") != 1 {
		t.Fatal("start replayed")
	}
}
func TestLostStartResponseCanBecomeReady(t *testing.T) {
	c, _, b := setup(t)
	b.startErr = errors.New("lost response")
	op := acquire(t, c, "request-1")
	ready(t, c, op)
	if b.count("start") != 1 {
		t.Fatal("start replayed")
	}
}
func TestDefinitiveStartRejectionAutomaticallyReleasesSlot(t *testing.T) {
	c, _, b := setup(t)
	b.delayedStart = true
	b.startErr = &lume.RejectedMutation{Status: 400}
	op := acquire(t, c, "rejected-start")
	for range 8 {
		tick(t, c)
	}
	l, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || l.State != "released" || l.StartPending || b.count("delete") != 1 {
		t.Fatal(l, err, b.counts)
	}
	release, err := c.Release(context.Background(), l.ID, "repeat-release")
	if err != nil || release.Job.Kind != "cleanup" || release.Lease.State != "released" {
		t.Fatal(release, err)
	}
}
func TestReleaseWaitsForDelayedStart(t *testing.T) {
	c, _, b := setup(t)
	b.delayedStart = true
	op := acquire(t, c, "request-1")
	tick(t, c)
	tick(t, c)
	if _, err := c.Release(context.Background(), op.Lease.ID, "release-1"); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	if b.count("delete") != 0 {
		t.Fatal("deleted disk with start pending")
	}
	b.mu.Lock()
	v := b.vms[op.Lease.Key()]
	v.State = "running"
	b.vms[op.Lease.Key()] = v
	b.mu.Unlock()
	for range 3 {
		tick(t, c)
	}
	l, _ := c.Lease(context.Background(), op.Lease.ID)
	if l.State != "released" {
		t.Fatal(l)
	}
}
func TestExpiryCleanupAndRenewal(t *testing.T) {
	c, _, _ := setup(t)
	op := acquire(t, c, "request-1")
	ready(t, c, op)
	extended := time.Now().Add(2 * time.Hour)
	l, err := c.Renew(context.Background(), op.Lease.ID, extended)
	if err != nil {
		t.Fatal(err)
	}
	l, err = c.Renew(context.Background(), op.Lease.ID, extended.Add(-time.Hour))
	if err != nil || !l.ExpiresAt.Equal(extended) {
		t.Fatalf("%+v %v", l, err)
	}
	c.now = func() time.Time { return extended.Add(time.Second) }
	for range 4 {
		tick(t, c)
	}
	l, _ = c.Lease(context.Background(), op.Lease.ID)
	if l.State != "released" {
		t.Fatal(l)
	}
	_, err = c.Renew(context.Background(), l.ID, extended.Add(time.Hour))
	code(t, err, "lease_expired")
}
func TestCleanupDeadlinePausesAcrossBackendOutage(t *testing.T) {
	c, _, b := setup(t)
	op := acquire(t, c, "outage-acquire")
	ready(t, c, op)
	cleanup, err := c.Release(context.Background(), op.Lease.ID, "outage-release")
	if err != nil {
		t.Fatal(err)
	}
	b.err = errors.New("backend unavailable")
	c.now = func() time.Time { return cleanup.Job.Deadline.Add(time.Minute) }
	tick(t, c)
	b.err = nil
	tick(t, c)
	j, err := c.Job(context.Background(), cleanup.Job.ID)
	if err != nil || j.State == "needs_attention" || !j.Deadline.After(c.now()) {
		t.Fatal(j, err)
	}
	for range 4 {
		tick(t, c)
	}
	l, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || l.State != "released" {
		t.Fatal(l, err)
	}
}
func TestSlowCloneDoesNotBlockStatusOrLoseRenewal(t *testing.T) {
	c, _, b := setup(t)
	b.blockClone = make(chan struct{})
	op := acquire(t, c, "request-1")
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { close(b.blockClone); c.wg.Wait() }()
	done := make(chan error, 1)
	go func() { _, err := c.Status(context.Background()); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("status blocked on clone")
	}
	extended := time.Now().Add(2 * time.Hour)
	if _, err := c.Renew(context.Background(), op.Lease.ID, extended); err != nil {
		t.Fatal(err)
	}
	_, err := c.Release(context.Background(), op.Lease.ID, "release-1")
	code(t, err, "operation_in_progress")
}

func TestCrossStorageCloneUsesExtendedMutationTimeout(t *testing.T) {
	l := domain.Lease{Location: "external", Source: &domain.Template{Location: "home"}}
	if got := mutationTimeout(l, "clone_dispatched"); got != 10*time.Minute {
		t.Fatalf("cross-storage clone timeout %s", got)
	}
	if got := mutationTimeout(l, "start_dispatched"); got != 2*time.Minute {
		t.Fatalf("start timeout %s", got)
	}
	l.Location = "home"
	if got := mutationTimeout(l, "clone_dispatched"); got != 2*time.Minute {
		t.Fatalf("same-storage clone timeout %s", got)
	}
}
func TestGracefulShutdownLetsClonePersistConfirmedOutcome(t *testing.T) {
	c, _, b := setup(t)
	b.blockClone = make(chan struct{})
	op := acquire(t, c, "shutdown-clone")
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx) }()
	deadline := time.After(3 * time.Second)
	for b.count("clone") == 0 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("clone was not dispatched")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		t.Fatal("daemon returned before the clone finished", err)
	default:
	}
	close(b.blockClone)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not drain completed clone")
	}
	l, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || !l.CloneConfirmed || l.State == "quarantined" {
		t.Fatal("clone outcome was not persisted before shutdown", l, err)
	}
}
func TestDeleteFailureRetainsSlot(t *testing.T) {
	c, _, b := setup(t)
	op := acquire(t, c, "request-1")
	ready(t, c, op)
	b.deleteErr = errors.New("failure")
	if _, err := c.Release(context.Background(), op.Lease.ID, "release-1"); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		tick(t, c)
	}
	status, _ := c.Status(context.Background())
	if status.Capacity.Used != 1 {
		t.Fatal(status.Capacity)
	}
	if b.count("delete") != 1 {
		t.Fatal("delete replayed")
	}
}
func TestReadyDriftDoesNotClaimSuccess(t *testing.T) {
	c, _, b := setup(t)
	op := acquire(t, c, "request-1")
	ready(t, c, op)
	b.mu.Lock()
	v := b.vms[op.Lease.Key()]
	v.State = "stopped"
	b.vms[op.Lease.Key()] = v
	b.mu.Unlock()
	tick(t, c)
	l, _ := c.Lease(context.Background(), op.Lease.ID)
	if l.State != "ready" {
		t.Fatalf("one transient observation changed lease state: %s", l.State)
	}
	tick(t, c)
	l, _ = c.Lease(context.Background(), op.Lease.ID)
	if l.State != "needs_attention" || l.Error.Code != "vm_drift" {
		t.Fatal(l)
	}
}
func TestReadyDriftCounterResetsAfterHealthyObservation(t *testing.T) {
	c, _, b := setup(t)
	op := acquire(t, c, "drift-reset")
	ready(t, c, op)
	key := op.Lease.Key()
	b.mu.Lock()
	vm := b.vms[key]
	vm.State = "stopped"
	b.vms[key] = vm
	b.mu.Unlock()
	tick(t, c)
	b.mu.Lock()
	vm.State = "running"
	b.vms[key] = vm
	b.mu.Unlock()
	tick(t, c)
	b.mu.Lock()
	vm.State = "stopped"
	b.vms[key] = vm
	b.mu.Unlock()
	tick(t, c)
	l, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || l.State != "ready" {
		t.Fatalf("separate transient observations caused drift: %s, %v", l.State, err)
	}
}
func TestReleaseBeforeCloneCannotDeleteCollision(t *testing.T) {
	c, _, b := setup(t)
	op := acquire(t, c, "request-1")
	b.mu.Lock()
	b.vms[op.Lease.Key()] = domain.VM{Name: op.Lease.VMName, Location: "home", State: "stopped"}
	b.mu.Unlock()
	if _, err := c.Release(context.Background(), op.Lease.ID, "release-1"); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	if b.count("delete") != 0 {
		t.Fatal("unowned disk deleted")
	}
}

func TestOperatorResolutionDoesNotReplayClone(t *testing.T) {
	c, _, b := setup(t)
	b.cloneErr = errors.New("lost response")
	op := acquire(t, c, "request-1")
	tick(t, c)
	tick(t, c) // Operator uses an inventory observed after clone completed.
	_, err := c.Resolve(context.Background(), op.Lease.ID, "resolve-1", op.Lease.VMName, false)
	code(t, err, "invalid_request")
	if _, err := c.Resolve(context.Background(), op.Lease.ID, "resolve-1", op.Lease.VMName, true); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		tick(t, c)
	}
	l, _ := c.Lease(context.Background(), op.Lease.ID)
	if l.State != "released" {
		t.Fatal(l)
	}
	if b.count("clone") != 1 || b.count("delete") != 1 {
		t.Fatal(b.counts)
	}
}
func TestExpiredPendingStartRemainsReserved(t *testing.T) {
	c, _, b := setup(t)
	b.delayedStart = true
	acquire(t, c, "request-1")
	tick(t, c)
	tick(t, c)
	now := time.Now().Add(2 * time.Hour)
	c.now = func() time.Time { return now }
	tick(t, c)
	now = now.Add(6 * time.Minute)
	tick(t, c)
	tick(t, c)
	status, _ := c.Status(context.Background())
	if status.Capacity.Used != 1 {
		t.Fatal(status)
	}
	if b.count("delete") != 0 {
		t.Fatal("deleted pending start")
	}
	if len(status.Jobs) != 1 || status.Jobs[0].State != "needs_attention" {
		t.Fatal(status.Jobs)
	}
}

func TestUnavailableStatusNeverAdvertisesFreeSlots(t *testing.T) {
	c, _, b := setup(t)
	b.err = errors.New("connection refused")
	tick(t, c)
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Capacity.Available != 0 || status.Observation.Error == nil {
		t.Fatal(status)
	}
}

func TestImageDeleteRequiresExactConfirmationAndNoLeases(t *testing.T) {
	c, _, b := setup(t)
	_, err := c.DeleteImage(context.Background(), "test", "image-delete-1", "wrong")
	code(t, err, "invalid_request")
	op := acquire(t, c, "request-1")
	_, err = c.DeleteImage(context.Background(), "test", "image-delete-1", "golden")
	code(t, err, "image_in_use")
	if _, err := c.Release(context.Background(), op.Lease.ID, "release-1"); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	deletion, err := c.DeleteImage(context.Background(), "test", "image-delete-1", "golden")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Acquire(context.Background(), "request-2", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "image_in_use")
	tick(t, c)
	tick(t, c)
	j, err := c.Job(context.Background(), deletion.Job.ID)
	if err != nil || j.State != "succeeded" {
		t.Fatalf("%+v %v", j, err)
	}
	replay, err := c.DeleteImage(context.Background(), "test", "image-delete-1", "golden")
	if err != nil || !replay.Replayed {
		t.Fatalf("%+v %v", replay, err)
	}
	if b.count("delete") != 1 {
		t.Fatal(b.counts)
	}
}
