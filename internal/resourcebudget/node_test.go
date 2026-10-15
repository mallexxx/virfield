package resourcebudget

import (
	"github.com/mallexxx/virfield/internal/domain"
	"testing"
)

func TestNodeRequiresBothFilesystemsAndDistinguishesMissingFromFull(t *testing.T) {
	r := domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100}
	report := domain.ResourceStatus{Limits: domain.ResourceLimits{CPU: 8, MemoryBytes: 32 << 30, DiskReserveBytes: 20}, DiskAvailable: map[string]int64{"host": 120}, Disks: []domain.StorageDisk{{Purpose: "overlay_disk", DeviceIdentity: "overlay", AvailableBytes: 120, Observed: true}, {Purpose: "golden_disk", DeviceIdentity: "golden", AvailableBytes: 119, Observed: true}}}
	s := Node("host", r, report, 0, false, "")
	if s.Allowed || s.ErrorCode != "resource_exhausted" {
		t.Fatal("Golden storage shortage hidden by overlay space", s)
	}
	for _, c := range s.Constraints {
		if c.Resource == "golden_disk" && (c.Failure != "insufficient_headroom" || *c.Headroom != -1) {
			t.Fatal(c)
		}
	}
	report.Disks[1].AvailableBytes = 120
	if s := Node("host", r, report, 0, false, ""); !s.Allowed {
		t.Fatal("Both exact boundaries should fit", s)
	}
	report.Disks = report.Disks[:1]
	s = Node("host", r, report, 0, false, "")
	if s.Allowed || s.ErrorCode != "resource_unknown" {
		t.Fatal("Missing golden observation treated as free space", s)
	}
	report.Disks[0].AvailableBytes = 0
	s = Node("host", r, report, 0, false, "")
	if s.Constraints[2].Headroom == nil || *s.Constraints[2].Headroom != -120 {
		t.Fatal("Known full overlay became unknown", s)
	}
}
