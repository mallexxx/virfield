package control

import (
	"fmt"

	"github.com/mallexxx/virfield/internal/domain"
)

type ResourceProbe func() (map[string]int64, error)

func (c *Controller) SetResources(l domain.ResourceLimits, p ResourceProbe) {
	c.resourceLimits = &l
	c.resourceProbe = p
}
func (c *Controller) resources(ls []domain.Lease) (domain.Resources, map[string]int64, error) {
	observed := 0
	for _, v := range c.observation.VMs {
		if v.State != "stopped" {
			observed++
		}
	}
	if c.observation.HostUsed > observed {
		return domain.Resources{}, nil, domain.Err("resource_unknown", "An active VM is missing from inventory; resource admission paused")
	}
	var sum domain.Resources
	disk := map[string]int64{}
	seen := map[string]bool{}
	add := func(key, location string, r domain.Resources) error {
		if seen[key] {
			return nil
		}
		seen[key] = true
		if r.CPU < 1 || r.MemoryBytes < 1 || r.DiskBytes < 1 {
			return domain.Err("resource_unknown", "Cannot verify VM resource allocation; admission paused")
		}
		sum.CPU += r.CPU
		sum.MemoryBytes += r.MemoryBytes
		sum.DiskBytes += r.DiskBytes
		disk[location] += r.DiskBytes
		return nil
	}
	for _, l := range ls {
		if l.Purpose == "image" {
			continue
		}
		r := l.Resources
		for _, v := range c.observation.VMs {
			if v.Key() == l.Key() {
				r = v.Resources
				break
			}
		}
		if err := add(l.Key(), l.Location, r); err != nil {
			return sum, disk, err
		}
	}
	for _, v := range c.observation.VMs {
		if v.State != "stopped" {
			if err := add(v.Key(), v.Location, v.Resources); err != nil {
				return sum, disk, err
			}
		}
	}
	return sum, disk, nil
}
func (c *Controller) admitResources(ls []domain.Lease, t domain.Template) (domain.Resources, error) {
	var request domain.Resources
	for _, v := range c.observation.VMs {
		if v.Key() == t.Location+"/"+t.Name {
			request = v.Resources
		}
	}
	if c.resourceLimits == nil {
		return request, nil
	}
	if c.resourceError != "" {
		return request, domain.Err("resource_unavailable", c.resourceError)
	}
	sum, _, err := c.resources(ls)
	if err != nil {
		return request, err
	}
	lim := *c.resourceLimits
	if request.CPU < 1 || request.MemoryBytes < 1 || request.DiskBytes < 1 {
		return request, domain.Err("resource_unknown", "Source VM resources are unavailable")
	}
	if sum.CPU+request.CPU > lim.CPU {
		return request, domain.Err("resource_exhausted", fmt.Sprintf("CPU quota exceeded: %d reserved + %d requested, limit %d", sum.CPU, request.CPU, lim.CPU))
	}
	if sum.MemoryBytes+request.MemoryBytes > lim.MemoryBytes {
		return request, domain.Err("resource_exhausted", fmt.Sprintf("RAM quota exceeded: %d GiB reserved + %d GiB requested, limit %d GiB", sum.MemoryBytes>>30, request.MemoryBytes>>30, lim.MemoryBytes>>30))
	}
	available, ok := c.diskAvailable[t.Location]
	if !ok || available < sum.DiskBytes+request.DiskBytes+lim.DiskReserveBytes {
		return request, domain.Err("resource_exhausted", "Insufficient free disk for reserved VM growth and host safety reserve")
	}
	return request, nil
}
