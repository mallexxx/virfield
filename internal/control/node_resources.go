package control

import (
	"math"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
	budget "github.com/mallexxx/virfield/internal/resourcebudget"
)

// Node VM admission has its own budget; physical account leases retain their
// exclusive slot. These limits reserve VM growth, never prune operator images.
type NodeResourcePolicy struct {
	Limits          *domain.ResourceLimits
	VMResources     domain.Resources
	CacheLimitBytes int64
}

func (c *Controller) SetNodeResources(policies map[string]NodeResourcePolicy) {
	c.nodeResourcePolicies = policies
}

func (c *Controller) nodeResourceUsage(ls []domain.Lease, location string) (domain.ResourceStatus, int64, error) {
	policy := c.nodeResourcePolicies[location]
	report := domain.ResourceStatus{CacheLimitBytes: policy.CacheLimitBytes, DiskAvailable: map[string]int64{}}
	_, hosts := c.nodeHosts()
	h := hosts[location]
	report.Limits = domain.ResourceLimits{CPU: h.Capacity.Total.CPU, MemoryBytes: h.Capacity.Total.MemoryBytes - (4 << 30), DiskReserveBytes: 20 << 30}
	if policy.Limits != nil {
		report.Limits = *policy.Limits
		report.Limits.CPU = min(report.Limits.CPU, h.Capacity.Total.CPU)
		report.Limits.MemoryBytes = min(report.Limits.MemoryBytes, h.Capacity.Total.MemoryBytes)
	}
	for _, storage := range c.observation.StorageContainers {
		if storage.ID == location && storage.Availability == "available" {
			report.DiskAvailable[location] = storage.AvailableBytes
			report.CacheBytes = storage.CacheBytes
			report.Disks = storage.Disks
		}
	}
	var missing []string
	if report.Limits.CPU < 1 {
		missing = append(missing, "CPU capacity/limit")
	}
	if report.Limits.MemoryBytes < 1 {
		missing = append(missing, "RAM capacity/limit")
	}
	if free, ok := report.DiskAvailable[location]; !ok || free < 0 {
		missing = append(missing, "storage free bytes for "+location)
	}
	if report.CacheBytes < 0 {
		missing = append(missing, "image-cache allocated bytes")
	}
	if len(missing) > 0 {
		return report, 0, domain.Err("resource_unknown", "Node inventory unavailable: "+strings.Join(missing, ", "))
	}
	seen, physical := map[string]bool{}, map[string]bool{}
	var pendingCache int64
	add := func(key string, r domain.Resources) error {
		if seen[key] {
			return nil
		}
		seen[key] = true
		if r.CPU < 1 || r.MemoryBytes < 1 || r.DiskBytes < 1 {
			return domain.Err("resource_unknown", "Node VM resource reservation is unavailable")
		}
		if r.CPU > int(^uint(0)>>1)-report.Reserved.CPU || r.MemoryBytes > math.MaxInt64-report.Reserved.MemoryBytes || r.DiskBytes > math.MaxInt64-report.Reserved.DiskBytes {
			return domain.Err("resource_unknown", "Node resource reservation arithmetic overflow; admission paused")
		}
		report.Reserved.CPU += r.CPU
		report.Reserved.MemoryBytes += r.MemoryBytes
		report.Reserved.DiskBytes += r.DiskBytes
		return nil
	}
	for _, l := range ls {
		if l.Location != location || l.State == "released" || l.State == "image_ready" {
			continue
		}
		if l.Purpose != "image" && l.ImageID == "" {
			physical[l.Key()] = true
			continue
		}
		r := l.Resources
		for _, vm := range c.observation.VMs {
			if vm.Key() == l.Key() && vm.Resources.CPU > 0 && vm.Resources.MemoryBytes > 0 && vm.Resources.DiskBytes > 0 {
				r = vm.Resources
				break
			}
		}
		if err := add(l.Key(), r); err != nil {
			return report, pendingCache, err
		}
		if l.Purpose == "image" {
			pendingCache += r.DiskBytes
		}
	}
	for _, vm := range c.observation.VMs {
		if vm.Location == location && vm.State != "stopped" && !physical[vm.Key()] {
			if err := add(vm.Key(), vm.Resources); err != nil {
				return report, pendingCache, err
			}
		}
	}
	return report, pendingCache, nil
}

func (c *Controller) admitNodeResources(ls []domain.Lease, t domain.Template, building bool) (domain.Resources, error) {
	r, snapshot := c.nodeAdmission(ls, t, building)
	return r, budget.Error(snapshot)
}

func (c *Controller) nodeAdmission(ls []domain.Lease, t domain.Template, building bool) (domain.Resources, domain.AdmissionSnapshot) {
	policy, configured := c.nodeResourcePolicies[t.Location]
	s := domain.AdmissionSnapshot{Host: t.Location}
	if t.Image == nil || !configured {
		return t.Resources, budget.Finish(s, "")
	}
	r := policy.VMResources
	source := t.Name
	if building && t.Image.Node != nil {
		source = t.Image.Node.Golden
	}
	known := false
	for _, vm := range c.observation.VMs {
		if vm.Key() == t.Location+"/"+source && vm.Resources.DiskBytes > 0 {
			r.DiskBytes = max(r.DiskBytes, vm.Resources.DiskBytes)
			known = true
		}
	}
	unknown := ""
	if !known || r.CPU < 1 || r.MemoryBytes < 1 || r.DiskBytes < 1 {
		unknown = "Node source CPU/RAM/full potential disk size must be observed before cloning"
	}
	report, pendingCache, err := c.nodeResourceUsage(ls, t.Location)
	if err != nil {
		unknown = err.Error()
	}
	return r, budget.Node(t.Location, r, report, pendingCache, building, unknown)
}
