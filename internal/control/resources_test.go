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
