package baremetal

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
)

// Admission reserves durably in Control. Recheck real storage and VM allocation
// after wake, immediately before touching a clone, to reject stale cached data.
func (r *Runtime) checkVMResources(ctx context.Context, n Node, l domain.Lease, source string, building bool) error {
	if l.Resources.CPU == 0 {
		return nil
	} // Resume previously accepted legacy leases.
	p, err := r.probe(ctx, n, r.now())
	if err != nil {
		return err
	}
	request := l.Resources
	if source == "" {
		source = p.vms[l.VMName].Golden
	}
	configured := vmResources(n)
	if request.CPU < configured.CPU || request.MemoryBytes < configured.MemoryBytes || request.DiskBytes < configured.DiskBytes || request.DiskBytes < p.goldenDisks[source] || p.goldenDisks[source] < 1 {
		return domain.Err("resource_unknown", "Node source disk does not match its accepted resource reservation")
	}
	limits := domain.ResourceLimits{CPU: p.host.Capacity.Total.CPU, MemoryBytes: p.host.Capacity.Total.MemoryBytes - (4 << 30), DiskReserveBytes: 20 << 30}
	if n.VMs.ResourceLimits != nil {
		limits = *n.VMs.ResourceLimits
		limits.CPU = min(limits.CPU, p.host.Capacity.Total.CPU)
		limits.MemoryBytes = min(limits.MemoryBytes, p.host.Capacity.Total.MemoryBytes)
	}
	var used domain.Resources
	var pendingCache int64
	for name, vm := range p.vms {
		if name == l.VMName {
			continue
		}
		if vm.Resources.CPU < 1 || vm.Resources.MemoryBytes < 1 || vm.Resources.DiskBytes < 1 {
			return domain.Err("resource_unknown", "Node VM allocation cannot be verified before cloning")
		}
		used.CPU += vm.Resources.CPU
		used.MemoryBytes += vm.Resources.MemoryBytes
		used.DiskBytes += vm.Resources.DiskBytes
		if vm.Scope == "images" {
			pendingCache += vm.Resources.DiskBytes
		}
	}
	if used.CPU+request.CPU > limits.CPU || used.MemoryBytes+request.MemoryBytes > limits.MemoryBytes || used.DiskBytes+request.DiskBytes+limits.DiskReserveBytes > p.storage.AvailableBytes {
		return domain.Err("resource_exhausted", "Fresh node observation cannot fit the accepted VM reservation")
	}
	if building && n.VMs.CacheLimitBytes > 0 && p.storage.CacheBytes+pendingCache+request.DiskBytes > n.VMs.CacheLimitBytes {
		return domain.Err("resource_exhausted", "Node image cache quota changed before cloning")
	}
	return nil
}
