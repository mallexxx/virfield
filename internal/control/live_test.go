package control

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/hostlock"
	"github.com/mallexxx/virfield/internal/lume"
	"github.com/mallexxx/virfield/internal/store"
)

// TestLiveLifecycle is intentionally excluded from normal execution. It creates
// two disposable VMs and permanently deletes those exact leased VMs. The caller
// must explicitly authorize this test and provide a NEW persistent state dir.
// The journal is retained on both success and failure for operator recovery.
func TestLiveLifecycle(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_MUTATIONS") != "I_APPROVE_TEMPORARY_VM_DELETION" {
		t.Skip("requires explicit authorization to create, run and delete two disposable macOS VMs")
	}
	dir := os.Getenv("VIRFIELD_LIVE_STATE_DIR")
	name := os.Getenv("VIRFIELD_LIVE_TEMPLATE")
	if !filepath.IsAbs(dir) || !domain.ValidName(name) {
		t.Fatal("set absolute VIRFIELD_LIVE_STATE_DIR (new directory) and VIRFIELD_LIVE_TEMPLATE")
	}
	lock, err := hostlock.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	b, err := lume.New("http://127.0.0.1:7777")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	o, err := b.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.HostUsed != 0 {
		t.Fatal("live acceptance requires no running VMs; do not stop unrelated machines to make room")
	}
	for _, vm := range o.VMs {
		if vm.State != "stopped" {
			t.Fatal("all existing VMs must be stopped before this test")
		}
	}
	found := false
	for _, v := range o.VMs {
		if v.Name == name && v.Location == "home" && v.OS == "macOS" {
			found = true
		}
	}
	if !found {
		t.Fatal("template not found as a stopped macOS VM in home storage")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	tm := []domain.Template{{ID: "live-test", Name: name, Location: "home"}}
	c, err := New(db, b, tm, 2, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	leases := []domain.Operation{}
	// Cleanup uses only IDs accepted during this test. Unknown clone outcomes stay
	// quarantined and retain their journal; the test never invokes Resolve.
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 6*time.Minute)
		defer stop()
		c.wg.Wait()
		for _, op := range leases {
			l, err := c.Lease(cleanupCtx, op.Lease.ID)
			if err != nil {
				t.Error(err)
				continue
			}
			if l.State == "released" {
				continue
			}
			if _, err := c.Release(cleanupCtx, l.ID, "live-release-"+l.ID); err != nil {
				t.Errorf("cleanup needs operator inspection: %s: %v (journal: %s)", l.ID, err, dir)
			}
		}
		for {
			if err := c.Tick(cleanupCtx); err != nil {
				t.Error(err)
				break
			}
			c.wg.Wait()
			remaining, err := db.Leases(cleanupCtx)
			if err != nil {
				t.Error(err)
				break
			}
			if len(remaining) == 0 {
				break
			}
			attention := false
			for _, l := range remaining {
				if l.State == "quarantined" || l.State == "needs_attention" {
					attention = true
				}
			}
			if attention {
				t.Errorf("cleanup requires inspection; retained journal: %s", dir)
				break
			}
			select {
			case <-cleanupCtx.Done():
				t.Errorf("cleanup deadline; retained journal: %s", dir)
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	for _, key := range []string{"live-request-one", "live-request-two"} {
		op, err := c.Acquire(ctx, key, domain.AcquireRequest{Template: "live-test", TTLSeconds: 3600})
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, op)
		t.Logf("created lease %s, disposable VM %s", op.Lease.ID, op.Lease.VMName)
	}
	_, err = c.Acquire(ctx, "live-request-three", domain.AcquireRequest{Template: "live-test", TTLSeconds: 3600})
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "capacity_exhausted" {
		t.Fatalf("third request must be refused: %v", err)
	}
	for {
		if err := c.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		c.wg.Wait()
		allReady := true
		for _, op := range leases {
			l, err := c.Lease(ctx, op.Lease.ID)
			if err != nil {
				t.Fatal(err)
			}
			if l.State == "quarantined" || l.State == "needs_attention" {
				t.Fatalf("prepare requires inspection: %+v", l)
			}
			if l.State != "ready" {
				allReady = false
			}
		}
		if allReady {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	// Reopen the actual database and reconstruct the controller with no in-memory
	// leases. Crash boundaries are additionally tested with the fake backend.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	db = reopened
	recovered, err := New(db, b, tm, 2, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	c = recovered
	if err := c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"live-request-one", "live-request-two"} {
		op, err := c.Acquire(ctx, key, domain.AcquireRequest{Template: "live-test", TTLSeconds: 3600})
		if err != nil || !op.Replayed || op.Lease.ID != leases[i].Lease.ID {
			t.Fatalf("reopen replay failed: %+v %v", op, err)
		}
	}
	t.Log("Both VMs reached Lume-reported SSH readiness; third admission refused; database reopen preserved leases. Cleanup follows.")
}
