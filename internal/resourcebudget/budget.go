// Package resourcebudget evaluates the same quotas in controller admission and
// fresh node runtime rechecks. It performs no observation or mutation.
package resourcebudget

import (
	"fmt"
	"math"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

func Constraint(resource, location, unit string, limit, used, reserved, requested, safety int64, unknown string) domain.AdmissionConstraint {
	c := domain.AdmissionConstraint{Resource: resource, Location: location, Unit: unit, Limit: limit, ObservedUsed: used, Reserved: reserved, Requested: requested, SafetyReserve: safety, UnknownReason: unknown}
	if unknown != "" {
		c.Failure = "unknown_inventory"
		return c
	}
	total := int64(0)
	for _, n := range []int64{used, reserved, requested, safety} {
		if n < 0 || n > math.MaxInt64-total || limit < 0 {
			c.UnknownReason = "invalid or overflowing resource inventory"
			c.Failure = "unknown_inventory"
			return c
		}
		total += n
	}
	headroom := limit - total
	c.Headroom, c.Allowed = &headroom, headroom >= 0
	if !c.Allowed {
		c.Failure = "insufficient_headroom"
	}
	return c
}

func budgetAmount(n int64, unit string) string {
	if unit == "bytes" {
		return fmt.Sprintf("%d bytes (%.3f GB, %.3f GiB)", n, float64(n)/1e9, float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d %s", n, unit)
}

// Every candidate disk is reported, including those rejected before fallback.
// Disk constraints are alternatives; CPU, RAM and cache are shared requirements.
func Finish(s domain.AdmissionSnapshot, unknown string) domain.AdmissionSnapshot {
	s.Allowed = unknown == ""
	hasDisk, diskFits := false, false
	var details []string
	for _, c := range s.Constraints {
		label := c.Resource
		if c.Location != "" {
			label += "[" + c.Location + "]"
		}
		if c.UnknownReason != "" {
			details = append(details, label+": unavailable ("+c.UnknownReason+")")
		} else {
			basis := "limit"
			if c.Resource == "disk" || strings.HasSuffix(c.Resource, "_disk") {
				basis = "observed free"
			}
			details = append(details, fmt.Sprintf("%s: %s %s - observed used %s - reserved %s - requested full potential %s - safety reserve %s = headroom %s", label, basis, budgetAmount(c.Limit, c.Unit), budgetAmount(c.ObservedUsed, c.Unit), budgetAmount(c.Reserved, c.Unit), budgetAmount(c.Requested, c.Unit), budgetAmount(c.SafetyReserve, c.Unit), budgetAmount(*c.Headroom, c.Unit)))
		}
		if c.Resource == "disk" {
			hasDisk = true
			if c.Allowed {
				diskFits = true
				if s.SelectedLocation == "" {
					s.SelectedLocation = c.Location
				}
			}
		} else if !c.Allowed {
			s.Allowed = false
		}
	}
	if hasDisk && !diskFits {
		s.Allowed = false
	}
	if !s.Allowed {
		s.ErrorCode = "resource_exhausted"
		if unknown != "" {
			s.ErrorCode = "resource_unknown"
		}
		for _, c := range s.Constraints {
			if c.UnknownReason != "" && (c.Resource != "disk" || !diskFits) {
				s.ErrorCode = "resource_unknown"
			}
		}
	}
	if unknown != "" {
		details = append([]string{unknown}, details...)
	}
	s.Summary = strings.Join(details, "; ")
	if len(details) == 0 {
		s.Summary = "Resource quotas are not configured for this target"
	}
	return s
}

func Error(s domain.AdmissionSnapshot) error {
	if s.Allowed {
		return nil
	}
	return &domain.Error{Code: s.ErrorCode, Message: s.Summary, Admission: &s}
}
