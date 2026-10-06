package control

import (
	"fmt"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

type ResourceProbe func() (map[string]int64, error)

func (c *Controller) SetResources(l domain.ResourceLimits, p ResourceProbe) {
	c.resourceLimits = &l
	c.resourceProbe = p
}
func (c *Controller) SetCloneLocations(storagePaths map[string]string, defaultLocation string, fallbackLocations []string) error {
	allowed := make(map[string]bool, len(storagePaths))
	for name := range storagePaths {
		allowed[name] = true
	}
	locations := append([]string{defaultLocation}, fallbackLocations...)
	seen := map[string]bool{}
	for _, name := range locations {
		if name == "" {
			continue
		}
		if !allowed[name] {
			return fmt.Errorf("clone location %s is not in storage_paths", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate clone location %s", name)
		}
		seen[name] = true
	}
	c.storageLocations = allowed
	c.defaultCloneLocation = defaultLocation
	c.fallbackCloneLocations = append([]string(nil), fallbackLocations...)
	return nil
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
func (c *Controller) admitResources(ls []domain.Lease, t domain.Template, requestedLocation string) (domain.Resources, string, error) {
	var request domain.Resources
	for _, v := range c.observation.VMs {
		if v.Key() == t.Location+"/"+t.Name {
			request = v.Resources
		}
	}
	candidates, err := c.cloneLocationCandidates(t, requestedLocation)
	if err != nil {
		return request, "", err
	}
	if c.resourceLimits == nil {
		return request, candidates[0], nil
	}
	if c.resourceError != "" {
		return request, "", domain.Err("resource_unavailable", c.resourceError)
	}
	sum, diskReserved, err := c.resources(ls)
	if err != nil {
		return request, "", err
	}
	lim := *c.resourceLimits
	if request.CPU < 1 || request.MemoryBytes < 1 || request.DiskBytes < 1 {
		return request, "", domain.Err("resource_unknown", "Source VM resources are unavailable")
	}
	if sum.CPU+request.CPU > lim.CPU {
		return request, "", domain.Err("resource_exhausted", fmt.Sprintf("CPU quota exceeded: %d reserved + %d requested, limit %d", sum.CPU, request.CPU, lim.CPU))
	}
	if sum.MemoryBytes+request.MemoryBytes > lim.MemoryBytes {
		return request, "", domain.Err("resource_exhausted", fmt.Sprintf("RAM quota exceeded: %d GiB reserved + %d GiB requested, limit %d GiB", sum.MemoryBytes>>30, request.MemoryBytes>>30, lim.MemoryBytes>>30))
	}
	for _, location := range candidates {
		available, ok := c.diskAvailable[location]
		if ok && available >= diskReserved[location]+request.DiskBytes+lim.DiskReserveBytes {
			return request, location, nil
		}
	}
	return request, "", domain.Err("resource_exhausted", "No eligible clone storage has enough free disk for reserved VM growth and host safety reserve: "+strings.Join(candidates, ", "))
}

func (c *Controller) cloneLocationCandidates(t domain.Template, requestedLocation string) ([]string, error) {
	if requestedLocation != "" {
		if len(c.storageLocations) > 0 && !c.storageLocations[requestedLocation] {
			return nil, domain.Err("invalid_request", "destination_location is not in the operator storage allowlist")
		}
		if len(c.storageLocations) == 0 && requestedLocation != t.Location {
			return nil, domain.Err("invalid_request", "destination_location is not configured")
		}
		return []string{requestedLocation}, nil
	}
	first := c.defaultCloneLocation
	if first == "" {
		first = t.Location
	}
	candidates := []string{first}
	for _, location := range c.fallbackCloneLocations {
		if location != first {
			candidates = append(candidates, location)
		}
	}
	return candidates, nil
}
