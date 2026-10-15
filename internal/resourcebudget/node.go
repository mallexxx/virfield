package resourcebudget

import "github.com/mallexxx/virfield/internal/domain"

// Node applies identical constraints to admission and the fresh post-wake
// runtime probe. Separate overlay/golden devices are both requirements, even
// when they identify the same filesystem; their reservations are never added
// together. The aggregate fallback supports observations from older nodes.
func Node(host string, request domain.Resources, report domain.ResourceStatus, pendingCache int64, building bool, unknown string) domain.AdmissionSnapshot {
	s := domain.AdmissionSnapshot{Host: host, SelectedLocation: host}
	s.Constraints = append(s.Constraints,
		Constraint("cpu", "", "cores", int64(report.Limits.CPU), 0, int64(report.Reserved.CPU), int64(request.CPU), 0, unknown),
		Constraint("memory", "", "bytes", report.Limits.MemoryBytes, 0, report.Reserved.MemoryBytes, request.MemoryBytes, 0, unknown))
	if len(report.Disks) == 0 {
		if _, ok := report.DiskAvailable[host]; !ok {
			unknown = "storage free bytes have not been observed"
		}
		s.Constraints = append(s.Constraints, Constraint("disk", host, "bytes", report.DiskAvailable[host], 0, report.Reserved.DiskBytes, request.DiskBytes, report.Limits.DiskReserveBytes, unknown))
	} else {
		for _, purpose := range []string{"overlay_disk", "golden_disk"} {
			disk := domain.StorageDisk{Purpose: purpose}
			for _, observed := range report.Disks {
				if observed.Purpose == purpose {
					disk = observed
					break
				}
			}
			diskUnknown := unknown
			if !disk.Observed {
				diskUnknown = "Free bytes unavailable for " + disk.Purpose
			}
			s.Constraints = append(s.Constraints, Constraint(disk.Purpose, host+"/"+disk.DeviceIdentity, "bytes", disk.AvailableBytes, 0, report.Reserved.DiskBytes, request.DiskBytes, report.Limits.DiskReserveBytes, diskUnknown))
		}
	}
	if building && report.CacheLimitBytes > 0 {
		s.Constraints = append(s.Constraints, Constraint("image_cache", host, "bytes", report.CacheLimitBytes, report.CacheBytes, pendingCache, request.DiskBytes, 0, unknown))
	}
	return Finish(s, unknown)
}
