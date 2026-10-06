package control

import (
	"context"
	"errors"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestResourceAdmissionIsReservedAndFailsClosed(t *testing.T) {
	c, _, b := setup(t)
	v := b.vms["home/golden"]
	v.Resources = domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 80 << 30}
	b.vms[v.Key()] = v
	c.SetResources(domain.ResourceLimits{CPU: 6, MemoryBytes: 32 << 30, DiskReserveBytes: 20 << 30}, func() (map[string]int64, error) { return map[string]int64{"home": 500 << 30}, nil })
	tick(t, c)
	acquire(t, c, "resource-first")
	_, err := c.Acquire(context.Background(), "resource-second", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "resource_exhausted")
	c.resourceLimits.CPU = 12
	c.resourceLimits.MemoryBytes = 12 << 30
	_, err = c.Acquire(context.Background(), "resource-memory", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "resource_exhausted")
	c.resourceLimits.MemoryBytes = 32 << 30
	c.diskAvailable["home"] = 150 << 30
	_, err = c.Acquire(context.Background(), "resource-disk", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "resource_exhausted")
	c.resourceProbe = func() (map[string]int64, error) { return nil, errors.New("disk unavailable") }
	tick(t, c)
	_, err = c.Acquire(context.Background(), "resource-unknown", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "resource_unavailable")
}

func TestCloneStorageUsesDefaultAndFallbackLocations(t *testing.T) {
	c, _, b := setup(t)
	v := b.vms["home/golden"]
	v.Resources = domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 80 << 30}
	b.vms[v.Key()] = v
	if err := c.SetCloneLocations(map[string]string{"home": "/home", "fast": "/fast", "overflow": "/overflow"}, "fast", []string{"overflow", "home"}); err != nil {
		t.Fatal(err)
	}
	c.SetResources(domain.ResourceLimits{CPU: 12, MemoryBytes: 32 << 30, DiskReserveBytes: 20 << 30}, func() (map[string]int64, error) {
		return map[string]int64{"home": 500 << 30, "fast": 50 << 30, "overflow": 200 << 30}, nil
	})
	tick(t, c)

	op := acquire(t, c, "fallback-location")
	if op.Lease.Location != "overflow" {
		t.Fatalf("location=%q; want overflow", op.Lease.Location)
	}
}

func TestExplicitCloneStorageIsStrict(t *testing.T) {
	c, _, b := setup(t)
	v := b.vms["home/golden"]
	v.Resources = domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 80 << 30}
	b.vms[v.Key()] = v
	if err := c.SetCloneLocations(map[string]string{"home": "/home", "fast": "/fast", "overflow": "/overflow"}, "overflow", []string{"home"}); err != nil {
		t.Fatal(err)
	}
	c.SetResources(domain.ResourceLimits{CPU: 12, MemoryBytes: 32 << 30, DiskReserveBytes: 20 << 30}, func() (map[string]int64, error) {
		return map[string]int64{"home": 500 << 30, "fast": 50 << 30, "overflow": 200 << 30}, nil
	})
	tick(t, c)

	_, err := c.Acquire(context.Background(), "strict-location", domain.AcquireRequest{Template: "test", TTLSeconds: 3600, DestinationLocation: "fast"})
	code(t, err, "resource_exhausted")

	_, err = c.Acquire(context.Background(), "unknown-location", domain.AcquireRequest{Template: "test", TTLSeconds: 3600, DestinationLocation: "unknown"})
	code(t, err, "invalid_request")
}

func TestDiskReservationIsScopedToDestinationLocation(t *testing.T) {
	c, _, b := setup(t)
	v := b.vms["home/golden"]
	v.Resources = domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 80 << 30}
	b.vms[v.Key()] = v
	if err := c.SetCloneLocations(map[string]string{"home": "/home", "other": "/other"}, "other", nil); err != nil {
		t.Fatal(err)
	}
	c.SetResources(domain.ResourceLimits{CPU: 12, MemoryBytes: 32 << 30, DiskReserveBytes: 20 << 30}, func() (map[string]int64, error) {
		return map[string]int64{"home": 101 << 30, "other": 101 << 30}, nil
	})
	tick(t, c)

	first := acquire(t, c, "home-location")
	if first.Lease.Location != "other" {
		t.Fatalf("location=%q; want other", first.Lease.Location)
	}
	c.defaultCloneLocation = "home"
	second := acquire(t, c, "separate-location")
	if second.Lease.Location != "home" {
		t.Fatalf("location=%q; want home", second.Lease.Location)
	}
}
