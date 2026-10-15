package control

import (
	"fmt"
	"math"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/provider"
	budget "github.com/mallexxx/virfield/internal/resourcebudget"
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
		if r.CPU > int(^uint(0)>>1)-sum.CPU || r.MemoryBytes > math.MaxInt64-sum.MemoryBytes || r.DiskBytes > math.MaxInt64-sum.DiskBytes || r.DiskBytes > math.MaxInt64-disk[location] {
			return domain.Err("resource_unknown", "VM resource reservation arithmetic overflow; admission paused")
		}
		sum.CPU += r.CPU
		sum.MemoryBytes += r.MemoryBytes
		sum.DiskBytes += r.DiskBytes
		disk[location] += r.DiskBytes
		return nil
	}
	// Physical nodes are bounded by their own lease slot, not this Mac's budget.
	nodeLocations, _ := c.nodeHosts()
	for _, l := range ls {
		if nodeLocations[l.Location] != "" || l.Purpose == "image" && (l.State == "image_ready" || l.State == "released") {
			continue
		}
		r := l.Resources
		for _, v := range c.observation.VMs {
			if v.Key() == l.Key() && v.Resources.CPU > 0 && v.Resources.MemoryBytes > 0 && v.Resources.DiskBytes > 0 {
				r = v.Resources
				break
			}
		}
		if err := add(l.Key(), l.Location, r); err != nil {
			return sum, disk, err
		}
	}
	for _, v := range c.observation.VMs {
		if v.State != "stopped" && nodeLocations[v.Location] == "" {
			if err := add(v.Key(), v.Location, v.Resources); err != nil {
				return sum, disk, err
			}
		}
	}
	return sum, disk, nil
}
func (c *Controller) admitResources(ls []domain.Lease, t domain.Template, requestedLocation string) (domain.Resources, string, error) {
	if c.secondaryProvider(t.Provider) {
		// A physical node is bounded by its own lease slot (capacityOn), never by
		// this Mac's CPU, memory or clone storage.
		candidates, err := c.cloneLocationCandidates(t, requestedLocation)
		if err != nil {
			return domain.Resources{}, "", err
		}
		resources, err := c.admitNodeResources(ls, t, false)
		return resources, candidates[0], err
	}
	request := c.leaseResourceRequest(t)
	candidates, err := c.cloneLocationCandidates(t, requestedLocation)
	if err != nil {
		return request, "", err
	}
	return c.admitResourceBudget(ls, request, candidates)
}

func (c *Controller) leaseResourceRequest(t domain.Template) domain.Resources {
	request := t.Resources
	for _, v := range c.observation.VMs {
		if v.Key() == t.Location+"/"+t.Name {
			request = v.Resources
		}
	}
	return request
}

func (c *Controller) admitResourceBudget(ls []domain.Lease, request domain.Resources, candidates []string) (domain.Resources, string, error) {
	snapshot := c.primaryAdmission(ls, request, candidates)
	if err := budget.Error(snapshot); err != nil {
		return request, "", err
	}
	return request, snapshot.SelectedLocation, nil
}

func (c *Controller) cloneLocationCandidates(t domain.Template, requestedLocation string) ([]string, error) {
	if c.secondaryProvider(t.Provider) {
		if requestedLocation != "" && requestedLocation != t.Location {
			return nil, domain.Err("invalid_request", "A physical node template runs only on its own node")
		}
		return []string{t.Location}, nil
	}
	if requestedLocation != "" {
		if len(c.storageLocations) > 0 && !c.storageLocations[requestedLocation] {
			return nil, domain.Err("invalid_request", "destination_location is not in the operator storage allowlist")
		}
		if len(c.storageLocations) == 0 && requestedLocation != t.Location {
			return nil, domain.Err("invalid_request", "destination_location is not configured")
		}
		if provider.Name(c.backend) == "tart" && t.Source == "" && requestedLocation != t.Location {
			return nil, domain.Err("invalid_request", "A local Tart golden must be cloned in its source container: "+t.Location)
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
	if provider.Name(c.backend) == "tart" && t.Source == "" {
		eligible := []string{}
		for _, location := range candidates {
			if location == t.Location {
				eligible = append(eligible, location)
			}
		}
		if len(eligible) == 0 {
			return nil, domain.Err("invalid_request", "Configured clone destinations cannot clone this local Tart golden; select its source container: "+t.Location)
		}
		candidates = eligible
	}
	return candidates, nil
}
