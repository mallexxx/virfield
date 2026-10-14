package control

import "github.com/mallexxx/virfield/internal/domain"

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
		}
	}
	if report.Limits.CPU < 1 || report.Limits.MemoryBytes < 1 || report.DiskAvailable[location] < 1 {
		return report, 0, domain.Err("resource_unknown", "Node CPU/RAM/storage inventory must be observed before VM admission")
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
	policy, configured := c.nodeResourcePolicies[t.Location]
	if t.Image == nil || !configured {
		return t.Resources, nil
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
	if !known || r.CPU < 1 || r.MemoryBytes < 1 || r.DiskBytes < 1 {
		return r, domain.Err("resource_unknown", "Node source disk size must be observed before cloning")
	}
	report, pendingCache, err := c.nodeResourceUsage(ls, t.Location)
	if err != nil {
		return r, err
	}
	if report.Reserved.CPU+r.CPU > report.Limits.CPU || report.Reserved.MemoryBytes+r.MemoryBytes > report.Limits.MemoryBytes || report.Reserved.DiskBytes+r.DiskBytes+report.Limits.DiskReserveBytes > report.DiskAvailable[t.Location] {
		return r, domain.Err("resource_exhausted", "Node CPU/RAM/disk budget cannot fit another VM or image build")
	}
	if building && policy.CacheLimitBytes > 0 && report.CacheBytes+pendingCache+r.DiskBytes > policy.CacheLimitBytes {
		return r, domain.Err("resource_exhausted", "Node image cache quota cannot fit another golden's full potential size")
	}
	return r, nil
}
