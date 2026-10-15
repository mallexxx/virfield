package control

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
	"sync"
	"testing"
	"time"
)

func TestNodeVMPreparationBudgetPreservesLeaseTTL(t *testing.T) {
	c, template := nodeResourceController(t)
	op, err := c.Acquire(context.Background(), "node-cold-start-budget", domain.AcquireRequest{Template: template.ID, TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if op.Job.Deadline.Sub(op.Job.CreatedAt) != 20*time.Minute || op.Lease.ExpiresAt.Sub(op.Lease.CreatedAt) != time.Hour {
		t.Fatal("Node boot/keychain budget must not extend the lease TTL", op)
	}
	primary, _, _ := setup(t)
	op = acquire(t, primary, "primary-start-budget")
	if op.Job.Deadline.Sub(op.Job.CreatedAt) != 10*time.Minute {
		t.Fatal("Primary VM startup budget changed", op)
	}
}

func nodeResourceController(t *testing.T) (*Controller, domain.Template) {
	t.Helper()
	c, _, _ := nodeController(t)
	resources := domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100 << 30}
	policy := NodeResourcePolicy{VMResources: resources, Limits: &domain.ResourceLimits{CPU: 6, MemoryBytes: 24 << 30, DiskReserveBytes: 20 << 30}}
	c.SetNodeResources(map[string]NodeResourcePolicy{"mbp": policy})
	for i, h := range c.observation.Hosts {
		if h.ID == "mbp" {
			h.Capacity.Total = domain.Resources{CPU: 8, MemoryBytes: 32 << 30}
			h.Capacity.MaxVMs = 3
			c.observation.Hosts[i] = h
		}
	}
	for i, storage := range c.observation.StorageContainers {
		if storage.ID == "mbp" {
			storage.AvailableBytes = 500 << 30
			storage.CacheBytes = 50 << 30
			c.observation.StorageContainers[i] = storage
		}
	}
	template := domain.Template{ID: "node-secured", Name: "secured", Location: "mbp", Provider: "baremetal", OS: "macOS", GuestUser: "admin", Image: &domain.ImageProfile{Node: &domain.NodeSource{Golden: "bootstrap"}, MacOS: "14.8.9", Build: "23J631", DisableSIP: true}}
	c.templates[template.ID] = template
	for _, name := range []string{"secured", "bootstrap"} {
		c.observation.VMs = append(c.observation.VMs, domain.VM{Name: name, Location: "mbp", Provider: "baremetal", OS: "macOS", State: "stopped", Resources: resources})
	}
	image := domain.Lease{ID: "image-node-ready", Purpose: "image", Template: template.ID, VMName: template.Name, Location: template.Location, State: "image_ready", ImageManifest: imageFingerprint(template.Image)}
	if err := c.store.SaveImage(context.Background(), image, nil, "", "", "image.ready", "Fixture ready image", template); err != nil {
		t.Fatal(err)
	}
	return c, template
}

func TestNodeReservationsAreAtomicAndSurviveControllerRestart(t *testing.T) {
	c, template := nodeResourceController(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for _, key := range []string{"node-concurrent-a", "node-concurrent-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			op, err := c.Acquire(context.Background(), key, domain.AcquireRequest{Template: template.ID, TTLSeconds: 3600})
			if err != nil {
				code(t, err, "resource_exhausted")
				return
			}
			if op.Lease.Resources.CPU != 4 || op.Lease.Resources.DiskBytes != 100<<30 {
				t.Error("Reservation not persisted", op.Lease.Resources)
			}
			mu.Lock()
			accepted++
			mu.Unlock()
		}(key)
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatal("Concurrent admission exceeded CPU limit", accepted)
	}
	ts := []domain.Template{}
	for _, v := range c.templates {
		ts = append(ts, v)
	}
	restarted, err := New(c.store, c.backend, ts, c.limit, c.log)
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetNodeResources(c.nodeResourcePolicies)
	restarted.observation = c.observation
	restarted.lastObservedMono = c.lastObservedMono
	_, err = restarted.Acquire(context.Background(), "node-after-restart", domain.AcquireRequest{Template: template.ID, TTLSeconds: 3600})
	code(t, err, "resource_exhausted")
	if _, err := restarted.Acquire(context.Background(), "physical-separate", domain.AcquireRequest{Template: "mbp-monterey", TTLSeconds: 3600}); err != nil {
		t.Fatal("VM budget blocked independent host reservation", err)
	}
}

func TestNodeImageCacheAndDiskBudgetsCountUnfinishedBuilds(t *testing.T) {
	c, template := nodeResourceController(t)
	policy := c.nodeResourcePolicies["mbp"]
	policy.Limits.CPU = 8
	policy.CacheLimitBytes = 240 << 30
	c.nodeResourcePolicies["mbp"] = policy
	if _, err := c.admitNodeResources(nil, template, true); err != nil {
		t.Fatal(err)
	}
	pending := []domain.Lease{{ID: "image-pending", Purpose: "image", VMName: "building", Location: "mbp", State: "image_building", Resources: policy.VMResources}}
	_, err := c.admitNodeResources(pending, template, true)
	code(t, err, "resource_exhausted")
	policy.CacheLimitBytes = 300 << 30
	c.nodeResourcePolicies["mbp"] = policy
	c.observation.VMs = append(c.observation.VMs, domain.VM{Name: "building", Location: "mbp", State: "running", Resources: policy.VMResources})
	if _, err := c.admitNodeResources(pending, template, true); err != nil {
		t.Fatal("Observed build double counted its durable reservation", err)
	}
	for i, v := range c.observation.StorageContainers {
		if v.ID == "mbp" {
			c.observation.StorageContainers[i].AvailableBytes = 210 << 30
		}
	}
	_, err = c.admitNodeResources(pending, template, false)
	code(t, err, "resource_exhausted")
	for i, v := range c.observation.StorageContainers {
		if v.ID == "mbp" {
			c.observation.StorageContainers[i].AvailableBytes = 0
		}
	}
	_, err = c.admitNodeResources(nil, template, false)
	code(t, err, "resource_unknown")
}
