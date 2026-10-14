package control

import (
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// Packer image builds consume the same CPU/RAM/disk budget as worker leases.
// Reserve before the resolve/build dispatch, including portable publication.
func (c *Controller) admitImageResources(ls []domain.Lease, t domain.Template) (domain.Resources, error) {
	r := t.Resources
	if c.secondaryProvider(t.Provider) {
		return c.admitNodeResources(ls, t, true)
	}
	if c.resourceLimits == nil {
		return r, nil
	}
	if t.Image != nil && t.Image.Tart != nil {
		r.CPU = max(4, r.CPU)
		r.MemoryBytes = max(8<<30, r.MemoryBytes)
		disk := max(t.Resources.DiskBytes, t.Image.BuildDiskBytes)
		known := disk > 0
		// macOS 27 keeps the upstream disk instead of resizing it. A qualified
		// golden of this exact base supplies an observed bound; a new base must
		// declare disk_bytes in the operator's approved catalog.
		for _, source := range c.templates {
			if source.Image == nil || source.Image.Tart == nil || source.Image.Tart.Upstream != t.Image.Tart.Upstream {
				continue
			}
			for _, vm := range c.observation.VMs {
				if vm.Key() == source.Location+"/"+source.Name && vm.Resources.DiskBytes > 0 {
					disk = max(disk, vm.Resources.DiskBytes)
					known = true
				}
			}
		}
		if strings.Split(t.Image.MacOS, ".")[0] == "27" && !known {
			return r, domain.Err("resource_unknown", "New macOS 27 Tart base requires an operator disk_bytes bound before building")
		}
		r.DiskBytes = max(120<<30, disk)
	}
	admitted, _, err := c.admitResourceBudget(ls, r, []string{t.Location})
	return admitted, err
}
