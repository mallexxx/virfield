package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type transferExecutor struct {
	backend      *fakeBackend
	calls        int
	err          error
	discardErr   error
	discardCalls int
}

func (e *transferExecutor) DiscardImageCopy(_ context.Context, l domain.Lease) error {
	e.discardCalls++
	if e.discardErr != nil {
		return e.discardErr
	}
	e.backend.mu.Lock()
	defer e.backend.mu.Unlock()
	delete(e.backend.vms, l.Key())
	return nil
}

func (e *transferExecutor) TransferImage(_ context.Context, source, dest domain.Lease) (string, error) {
	e.calls++
	e.backend.mu.Lock()
	defer e.backend.mu.Unlock()
	e.backend.vms[dest.Key()] = domain.VM{Name: dest.VMName, Location: dest.Location, OS: "macOS", State: "stopped", Resources: dest.Resources}
	return strings.Repeat("a", 64), e.err
}

func transferController(t *testing.T) (*Controller, *transferExecutor, domain.Lease) {
	c, s, b := setup(t)
	ctx := context.Background()
	p := &domain.ImageProfile{Tart: &domain.TartSource{Upstream: "ghcr.io/test/macos@sha256:" + strings.Repeat("a", 64)}, MacOS: "12.7.6", Build: "21H1320", Provision: "base-v1"}
	tm := domain.Template{ID: "test", Name: "golden", Location: "home", Image: p, OS: "macOS", Arch: "arm64", GuestUser: "admin", GuestHome: "/Users/admin"}
	c.templates[tm.ID] = tm
	c.SetImageCatalog(nil, []string{"home", "external"})
	r := domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 120_000_000_000}
	b.vms["home/golden"] = domain.VM{Name: "golden", Location: "home", OS: "macOS", State: "stopped", Resources: r}
	l := domain.Lease{ID: "image-source", Purpose: "image", State: "image_ready", Template: tm.ID, VMName: tm.Name, Location: tm.Location, ImageManifest: imageFingerprint(p), GuestUser: tm.GuestUser}
	if err := s.Save(ctx, l, nil, "", "", "image.ready", "qualified"); err != nil {
		t.Fatal(err)
	}
	c.SetResources(domain.ResourceLimits{CPU: 12, MemoryBytes: 32 << 30, DiskReserveBytes: 20 << 30}, func() (map[string]int64, error) { return map[string]int64{"home": 1 << 40, "external": 1 << 40}, nil })
	e := &transferExecutor{backend: b}
	c.SetImageTransferer(e)
	tick(t, c)
	return c, e, l
}

func TestTransferRegistersOnlyAfterContentAndFreshInventory(t *testing.T) {
	c, e, source := transferController(t)
	ctx := context.Background()
	req := domain.ImageTransferRequest{Template: "test", ID: "copied", Location: "external"}
	op, err := c.TransferImage(ctx, req, "transfer-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.templates["copied"]; ok {
		t.Fatal("registered before copy")
	}
	_, err = c.Acquire(ctx, "source-during-copy", domain.AcquireRequest{Template: "test", TTLSeconds: 60})
	code(t, err, "image_in_use")
	_, err = c.DeleteImage(ctx, "test", "delete-during-copy", "golden")
	code(t, err, "image_in_use")
	replay, err := c.TransferImage(ctx, req, "transfer-one")
	if err != nil || !replay.Replayed || replay.Lease.ID != op.Lease.ID {
		t.Fatal(replay, err)
	}
	tick(t, c)
	if _, ok := c.templates["copied"]; ok {
		t.Fatal("registered without fresh runtime observation")
	}
	tick(t, c)
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "image_ready" || l.ImageManifest != source.ImageManifest || l.Source != nil || e.calls != 1 {
		t.Fatal(l, e.calls, err)
	}
	src, err := c.Lease(ctx, source.ID)
	if err != nil || src.State != "image_ready" || src.ImageManifest != source.ImageManifest {
		t.Fatal("source changed", src, err)
	}
	templates, err := c.store.Templates(ctx)
	if err != nil || len(templates) != 1 || templates[0].Location != "external" || imageFingerprint(templates[0].Image) != source.ImageManifest {
		t.Fatal(templates, err)
	}
	replay, err = c.TransferImage(ctx, req, "transfer-one")
	if err != nil || !replay.Replayed || replay.Lease.State != "image_ready" || e.calls != 1 {
		t.Fatal(replay, err)
	}
}

func TestTransferFailureAndCrashNeverReplayOrPublish(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "restart"}[crash], func(t *testing.T) {
			c, e, _ := transferController(t)
			ctx := context.Background()
			e.err = errors.New("disk unplugged")
			op, err := c.TransferImage(ctx, domain.ImageTransferRequest{Template: "test", ID: "copied", Location: "external"}, "transfer-failure")
			if err != nil {
				t.Fatal(err)
			}
			if crash {
				j := op.Job
				j.State, j.Phase = "running", "copy_dispatched"
				j.UpdatedAt = time.Now()
				if err := c.store.Save(ctx, op.Lease, &j, "", "", "image.transfer_started", "interrupted"); err != nil {
					t.Fatal(err)
				}
				if err := c.Recover(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				tick(t, c)
			}
			tick(t, c)
			l, err := c.Lease(ctx, op.Lease.ID)
			if err != nil || l.State != "quarantined" {
				t.Fatal(l, err)
			}
			if _, ok := c.templates["copied"]; ok {
				t.Fatal("published unverified partial")
			}
			want := 1
			if crash {
				want = 0
			}
			if e.calls != want {
				t.Fatal("replayed mutation", e.calls)
			}
			_, err = c.RecoverImage(ctx, l.ID, "retry-transfer", l.VMName, "retry", true)
			code(t, err, "unsafe_retry")
			if _, err := c.RecoverImage(ctx, l.ID, "discard-transfer", l.VMName, "delete", true); err != nil {
				t.Fatal(err)
			}
			tick(t, c)
			tick(t, c)
			l, err = c.Lease(ctx, l.ID)
			if err != nil || l.State != "released" || e.discardCalls != 1 {
				t.Fatal("partial cleanup not confirmed", l, e.discardCalls, err)
			}
		})
	}
}

func TestTransferCleanupFailureRetainsReservationEvenWithoutRuntimeConfig(t *testing.T) {
	c, e, _ := transferController(t)
	ctx := context.Background()
	e.err = errors.New("copy failed")
	op, err := c.TransferImage(ctx, domain.ImageTransferRequest{Template: "test", ID: "partial", Location: "external"}, "partial-transfer")
	if err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	delete(e.backend.vms, op.Lease.Key())
	tick(t, c)
	e.discardErr = errors.New("ownership marker missing")
	if _, err := c.RecoverImage(ctx, op.Lease.ID, "partial-cleanup", op.Lease.VMName, "delete", true); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	tick(t, c)
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "quarantined" {
		t.Fatal("forgot unproven partial disk", l, err)
	}
	_, err = c.Acquire(ctx, "source-after-failed-cleanup", domain.AcquireRequest{Template: "test", TTLSeconds: 60})
	code(t, err, "image_in_use")
}

func TestTransferRefusesInvalidSourceDestinationAndBudgetBeforeCopy(t *testing.T) {
	for _, scenario := range []string{"unknown", "same", "collision", "running", "unverified", "disk", "unknown-disk"} {
		t.Run(scenario, func(t *testing.T) {
			c, e, source := transferController(t)
			req := domain.ImageTransferRequest{Template: "test", ID: "copy", Location: "external"}
			switch scenario {
			case "unknown":
				req.Location = "foreign"
			case "same":
				req.Location = "home"
			case "collision":
				req.ID = "test"
			case "running":
				c.observation.VMs[0].State = "running"
			case "unverified":
				source.ImageManifest = "stale"
				c.store.Save(context.Background(), source, nil, "", "", "image.changed", "")
			case "disk":
				c.diskAvailable["external"] = source.Resources.DiskBytes
			case "unknown-disk":
				delete(c.diskAvailable, "external")
			}
			if _, err := c.TransferImage(context.Background(), req, "reject-transfer"); err == nil {
				t.Fatal("unsafe admission", scenario)
			}
			if e.calls != 0 {
				t.Fatal("copy before admission")
			}
		})
	}
}
