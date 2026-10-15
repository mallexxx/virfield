package control

import (
	"context"
	"sort"

	"github.com/mallexxx/virfield/internal/domain"
	budget "github.com/mallexxx/virfield/internal/resourcebudget"
)

func (c *Controller) primaryAdmission(ls []domain.Lease, request domain.Resources, candidates []string) domain.AdmissionSnapshot {
	s := domain.AdmissionSnapshot{Host: "primary"}
	for _, storage := range c.observation.StorageContainers {
		if len(candidates) > 0 && storage.ID == candidates[0] && storage.HostID != "" {
			s.Host = storage.HostID
			break
		}
	}
	if c.resourceLimits == nil {
		s.SelectedLocation = candidates[0]
		return budget.Finish(s, "")
	}
	sum, reserved, err := c.resources(ls)
	unknown := ""
	if err != nil {
		unknown = err.Error()
	}
	cpuUnknown, memoryUnknown := unknown, unknown
	if request.CPU < 1 {
		cpuUnknown = "Source VM CPU allocation is unavailable"
	}
	if request.MemoryBytes < 1 {
		memoryUnknown = "Source VM RAM allocation is unavailable"
	}
	lim := *c.resourceLimits
	s.Constraints = append(s.Constraints,
		budget.Constraint("cpu", "", "cores", int64(lim.CPU), 0, int64(sum.CPU), int64(request.CPU), 0, cpuUnknown),
		budget.Constraint("memory", "", "bytes", lim.MemoryBytes, 0, sum.MemoryBytes, request.MemoryBytes, 0, memoryUnknown))
	for _, location := range candidates {
		available, ok := c.diskAvailable[location]
		diskUnknown := unknown
		if c.resourceError != "" {
			diskUnknown = c.resourceError
		}
		if !ok {
			diskUnknown = "storage free bytes have not been observed"
		}
		if request.DiskBytes < 1 {
			diskUnknown = "Source VM full potential disk size is unavailable"
		}
		s.Constraints = append(s.Constraints, budget.Constraint("disk", location, "bytes", available, 0, reserved[location], request.DiskBytes, lim.DiskReserveBytes, diskUnknown))
	}
	s = budget.Finish(s, unknown)
	if c.resourceError != "" {
		s.ErrorCode = "resource_unavailable"
	}
	return s
}

func (c *Controller) resourceAdmissionStatus(ls []domain.Lease, templates []domain.Template) map[string]domain.TemplateAdmission {
	result := map[string]domain.TemplateAdmission{}
	for _, t := range templates {
		report := domain.TemplateAdmission{InventoryVerified: c.healthy() == nil, Destinations: map[string]domain.AdmissionSnapshot{}}
		report.Acquire = c.acquireAdmissionSnapshot(ls, t, "")
		locations := []string{t.Location}
		if !c.secondaryProvider(t.Provider) && len(c.storageLocations) > 0 {
			locations = nil
			for location := range c.storageLocations {
				locations = append(locations, location)
			}
			sort.Strings(locations)
		}
		for _, location := range locations {
			report.Destinations[location] = c.acquireAdmissionSnapshot(ls, t, location)
		}
		if t.Image != nil {
			r, err := c.admitImageResources(ls, t)
			s := c.primaryAdmission(ls, r, []string{t.Location})
			if c.secondaryProvider(t.Provider) {
				_, s = c.nodeAdmission(ls, t, true)
			}
			if err != nil {
				s = admissionFailure(s, err)
			}
			report.ImageBuild = &s
		}
		result[t.ID] = report
	}
	return result
}

func admissionFailure(s domain.AdmissionSnapshot, err error) domain.AdmissionSnapshot {
	if e, ok := err.(*domain.Error); ok && e.Admission != nil {
		return *e.Admission
	}
	s.Allowed = false
	s.ErrorCode = "resource_unknown"
	if e, ok := err.(*domain.Error); ok {
		s.ErrorCode = e.Code
	}
	s.Summary = err.Error() + "; " + s.Summary
	return s
}

func (c *Controller) acquireAdmissionSnapshot(ls []domain.Lease, t domain.Template, location string) domain.AdmissionSnapshot {
	candidates, err := c.cloneLocationCandidates(t, location)
	if err != nil {
		return admissionFailure(domain.AdmissionSnapshot{Host: t.Location}, err)
	}
	if c.secondaryProvider(t.Provider) {
		_, s := c.nodeAdmission(ls, t, false)
		return s
	}
	r := c.leaseResourceRequest(t)
	return c.primaryAdmission(ls, r, candidates)
}

// Recheck the existing reservation before a mutation. Omit only this lease;
// all other owned/external reservations remain counted by the same evaluator.
// Persist the first refusal rather than writing an event on every polling tick.
func (c *Controller) recheckResources(ctx context.Context, l domain.Lease, j domain.Job) (bool, error) {
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return false, err
	}
	other := make([]domain.Lease, 0, len(ls))
	for _, item := range ls {
		if item.ID != l.ID {
			other = append(other, item)
		}
	}
	s := c.primaryAdmission(other, l.Resources, []string{l.Location})
	if c.secondaryProvider(l.Provider) {
		t, ok := c.templates[l.Template]
		if l.Source != nil {
			t = *l.Source
			ok = true
		}
		if !ok {
			return false, domain.Err("template_unavailable", "Cannot recheck missing node profile")
		}
		_, s = c.nodeAdmission(other, t, l.Purpose == "image")
	}
	if s.Allowed {
		return true, nil
	}
	if j.Error != nil && j.Error.Admission != nil {
		return false, nil
	}
	fault := &domain.Error{Code: s.ErrorCode, Message: s.Summary, Retryable: true, Admission: &s}
	l.Error, j.Error = fault, fault
	j.Progress = "Waiting for resources; original reservation and deadline retained"
	return false, c.save(ctx, l, j, "job.resource_wait", s.Summary)
}
