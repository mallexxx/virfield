package baremetal

import (
	"context"
	"fmt"
	"math"

	"github.com/mallexxx/virfield/internal/domain"
	budget "github.com/mallexxx/virfield/internal/resourcebudget"
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
	unknown := ""
	if request.CPU < configured.CPU || request.MemoryBytes < configured.MemoryBytes || request.DiskBytes < configured.DiskBytes || request.DiskBytes < p.goldenDisks[source] || p.goldenDisks[source] < 1 {
		unknown = fmt.Sprintf("Node source/reservation mismatch: observed source disk %d bytes, accepted full potential %d bytes, configured bound %d bytes", p.goldenDisks[source], request.DiskBytes, configured.DiskBytes)
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
			unknown = "Node VM allocation cannot be verified before cloning"
			continue
		}
		if vm.Resources.CPU > int(^uint(0)>>1)-used.CPU || vm.Resources.MemoryBytes > math.MaxInt64-used.MemoryBytes || vm.Resources.DiskBytes > math.MaxInt64-used.DiskBytes {
			unknown = "Node resource reservation arithmetic overflow; admission paused"
			continue
		}
		used.CPU += vm.Resources.CPU
		used.MemoryBytes += vm.Resources.MemoryBytes
		used.DiskBytes += vm.Resources.DiskBytes
		if vm.Scope == "images" {
			pendingCache += vm.Resources.DiskBytes
		}
	}
	report := domain.ResourceStatus{Limits: limits, Reserved: used, DiskAvailable: map[string]int64{n.ID: p.storage.AvailableBytes}, Disks: p.storage.Disks, CacheBytes: p.storage.CacheBytes, CacheLimitBytes: n.VMs.CacheLimitBytes}
	return budget.Error(budget.Node(n.ID, request, report, pendingCache, building, unknown))
}
