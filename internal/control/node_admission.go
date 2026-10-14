package control

import "github.com/mallexxx/virfield/internal/domain"

// A node's host reservation and VM pool are separate resources. Increasing its
// VM capacity must never make the physical account concurrently leasable.
func (c *Controller) admitNodeTarget(ls []domain.Lease, t domain.Template) error {
	if !c.secondaryProvider(t.Provider) {
		return nil
	}
	hostKeys := map[string]bool{}
	vmKeys := map[string]bool{}
	for _, l := range ls {
		if l.Location != t.Location || l.State == "released" || l.State == "image_ready" {
			continue
		}
		if l.Purpose != "image" && l.ImageID == "" {
			hostKeys[l.Key()] = true
		} else {
			vmKeys[l.Key()] = true
		}
	}
	if t.Image == nil {
		if len(hostKeys) != 0 {
			return domain.Err("capacity_exhausted", "Physical host already has an unreleased reservation")
		}
		return nil
	}
	_, hosts := c.nodeHosts()
	limit := hosts[t.Location].Capacity.MaxVMs - 1
	for _, vm := range c.observation.VMs {
		if vm.Location == t.Location && vm.State != "stopped" && !hostKeys[vm.Key()] {
			vmKeys[vm.Key()] = true
		}
	}
	if limit < 1 || len(vmKeys) >= limit {
		return domain.Err("capacity_exhausted", "Node VM pool has no available slot; its physical-host slot cannot be used by a VM")
	}
	return nil
}
