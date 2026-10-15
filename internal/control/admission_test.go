package control

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	budget "github.com/mallexxx/virfield/internal/resourcebudget"
)

func constraint(t *testing.T, s domain.AdmissionSnapshot, resource, location string) domain.AdmissionConstraint {
	t.Helper()
	for _, c := range s.Constraints {
		if c.Resource == resource && c.Location == location {
			return c
		}
	}
	t.Fatalf("Missing %s/%s: %+v", resource, location, s)
	return domain.AdmissionConstraint{}
}

func TestAdmissionDiskBoundaryFallbackAndUnknown(t *testing.T) {
	c, _, _ := setup(t)
	c.SetResources(domain.ResourceLimits{CPU: 8, MemoryBytes: 32 << 30, DiskReserveBytes: 20}, nil)
	r := domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100}
	ls := []domain.Lease{{ID: "lease-existing", VMName: "existing", Location: "first", State: "pending", Resources: r}}
	c.diskAvailable = map[string]int64{"first": 219, "second": 120}
	s := c.primaryAdmission(ls, r, []string{"first", "second"})
	if !s.Allowed || s.SelectedLocation != "second" {
		t.Fatal(s)
	}
	a, b := constraint(t, s, "disk", "first"), constraint(t, s, "disk", "second")
	if a.Reserved != 100 || a.Requested != 100 || a.SafetyReserve != 20 || *a.Headroom != -1 || *b.Headroom != 0 {
		t.Fatal(a, b)
	}
	_, location, err := c.admitResourceBudget(ls, r, []string{"first", "second"})
	if err != nil || location != "second" {
		t.Fatal(location, err)
	}
	delete(c.diskAvailable, "second")
	s = c.primaryAdmission(ls, r, []string{"first", "second"})
	if s.Allowed || s.ErrorCode != "resource_unknown" || constraint(t, s, "disk", "second").Headroom != nil {
		t.Fatal(s)
	}
	c.diskAvailable["second"] = 119
	_, _, err = c.admitResourceBudget(ls, r, []string{"first", "second"})
	fault, ok := err.(*domain.Error)
	if !ok || fault.Code != "resource_exhausted" || fault.Admission == nil {
		t.Fatal(err)
	}
	if !strings.Contains(fault.Message, "119 bytes") || !strings.Contains(fault.Message, "GB,") || !strings.Contains(fault.Message, "GiB)") {
		t.Fatal(fault.Message)
	}
	data, _ := json.Marshal(fault)
	var roundtrip domain.Error
	if err := json.Unmarshal(data, &roundtrip); err != nil || !reflect.DeepEqual(roundtrip.Admission, fault.Admission) {
		t.Fatal(string(data), err)
	}
	c.resourceError = "Cannot verify storage free space; admission paused"
	s = c.primaryAdmission(ls, r, []string{"first", "second"})
	if s.Allowed || s.ErrorCode != "resource_unavailable" || constraint(t, s, "cpu", "").Headroom == nil || constraint(t, s, "disk", "first").Headroom != nil {
		t.Fatal("Storage outage erased known CPU arithmetic", s)
	}
}

func TestFreshRuntimeAdmissionFailureRemainsVisibleWithoutReplay(t *testing.T) {
	c, s, b := setup(t)
	snapshot := domain.AdmissionSnapshot{Host: "node", Summary: "golden_disk: observed free 119 bytes - requested full potential 100 bytes - safety reserve 20 bytes = headroom -1 bytes"}
	b.cloneErr = &domain.Error{Code: "resource_exhausted", Message: snapshot.Summary, Admission: &snapshot}
	op := acquire(t, c, "fresh-node-refusal")
	tick(t, c)
	l, err := s.Lease(context.Background(), op.Lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if l.State != "quarantined" || l.Error == nil || l.Error.Code != "clone_outcome_unknown" || l.Error.Admission == nil || l.Error.Admission.Summary != snapshot.Summary || !strings.Contains(l.Error.Message, "119 bytes") {
		t.Fatal(l)
	}
	tick(t, c)
	if b.count("clone") != 1 {
		t.Fatal("Typed quota refusal incorrectly replayed a create")
	}
}

func TestStatusMatchesRefusalAfterConcurrentReservation(t *testing.T) {
	c, _, b := setup(t)
	v := b.vms["home/golden"]
	v.Resources = domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100}
	b.vms[v.Key()] = v
	c.SetResources(domain.ResourceLimits{CPU: 6, MemoryBytes: 32 << 30, DiskReserveBytes: 20}, func() (map[string]int64, error) { return map[string]int64{"home": 500}, nil })
	tick(t, c)
	acquire(t, c, "admission-first")
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := status.ResourceAdmission["test"]
	if !s.InventoryVerified || s.Acquire.Allowed || *constraint(t, s.Acquire, "cpu", "").Headroom != -2 {
		t.Fatal(s)
	}
	_, err = c.Acquire(context.Background(), "admission-second", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	fault, ok := err.(*domain.Error)
	if !ok || fault.Admission == nil || !reflect.DeepEqual(*fault.Admission, s.Acquire) {
		t.Fatal(err, s)
	}
}

func TestNodeCacheSnapshotCountsPendingBuildAndObservedCache(t *testing.T) {
	c, template := nodeResourceController(t)
	policy := c.nodeResourcePolicies["mbp"]
	policy.Limits.CPU = 8
	policy.CacheLimitBytes = 250 << 30
	c.nodeResourcePolicies["mbp"] = policy
	ls := []domain.Lease{{ID: "image-pending", Purpose: "image", VMName: "building", Location: "mbp", State: "image_building", Resources: policy.VMResources}}
	_, s := c.nodeAdmission(ls, template, true)
	cache := constraint(t, s, "image_cache", "mbp")
	if !s.Allowed || cache.ObservedUsed != 50<<30 || cache.Reserved != 100<<30 || cache.Requested != 100<<30 || *cache.Headroom != 0 {
		t.Fatal(s)
	}
	policy.CacheLimitBytes--
	c.nodeResourcePolicies["mbp"] = policy
	_, s = c.nodeAdmission(ls, template, true)
	if s.Allowed || *constraint(t, s, "image_cache", "mbp").Headroom != -1 {
		t.Fatal(s)
	}
	_, worker := c.nodeAdmission(ls, template, false)
	if !worker.Allowed {
		t.Fatal("Worker clone wrongly charged image cache", worker)
	}
}

func TestAdmissionArithmeticOverflowFailsClosed(t *testing.T) {
	c := budget.Constraint("disk", "overflow", "bytes", math.MaxInt64, 0, math.MaxInt64, 1, 0, "")
	s := budget.Finish(domain.AdmissionSnapshot{Constraints: []domain.AdmissionConstraint{c}}, "")
	if s.Allowed || s.ErrorCode != "resource_unknown" || c.Headroom != nil || c.UnknownReason == "" {
		t.Fatal(s)
	}
}

func TestAdmissionRecheckWaitsWithoutMutationAndResumesSameReservation(t *testing.T) {
	c, s, b := setup(t)
	v := b.vms["home/golden"]
	v.Resources = domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100}
	b.vms[v.Key()] = v
	free := int64(120)
	c.SetResources(domain.ResourceLimits{CPU: 8, MemoryBytes: 32 << 30, DiskReserveBytes: 20}, func() (map[string]int64, error) { return map[string]int64{"home": free}, nil })
	tick(t, c)
	op := acquire(t, c, "recheck-original")
	free = 119
	tick(t, c)
	l, err := s.Lease(context.Background(), op.Lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.count("clone") != 0 || l.Error == nil || l.Error.Admission == nil || *constraint(t, *l.Error.Admission, "disk", "home").Headroom != -1 {
		t.Fatal(l, b.count("clone"))
	}
	js, err := s.Jobs(context.Background())
	if err != nil || len(js) != 1 {
		t.Fatal(js, err)
	}
	updated := js[0].UpdatedAt
	tick(t, c)
	js, _ = s.Jobs(context.Background())
	if !js[0].UpdatedAt.Equal(updated) {
		t.Fatal("Repeated poll rewrote the same refusal")
	}
	free = 120
	tick(t, c)
	l, _ = s.Lease(context.Background(), op.Lease.ID)
	if b.count("clone") != 1 || l.Error != nil || l.ID != op.Lease.ID || !l.ExpiresAt.Equal(op.Lease.ExpiresAt) {
		t.Fatal(l, b.count("clone"))
	}
	// The clone is stopped; its full potential is counted once at start recheck.
	free = 119
	tick(t, c)
	if b.count("start") != 0 {
		t.Fatal("Started despite changed disk headroom")
	}
	free = 120
	tick(t, c)
	if b.count("start") != 1 {
		t.Fatal("Exact-boundary recheck failed to resume")
	}
}
