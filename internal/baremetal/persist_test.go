package baremetal

import (
	"context"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

// A restart while a node sleeps must not forget its golden or its leases:
// the asleep node stays admissible and its leases stay in inventory.
func TestLastProbeSurvivesRestartWhileNodeSleeps(t *testing.T) {
	dir := t.TempDir()
	htpc := Node{ID: "htpc", Address: "127.0.0.1", User: "bazzite", HostKeys: []string{testHostKey}, OS: "linux", Arch: "amd64", WakeMAC: "B0:6E:BF:60:6D:64", WakeBroadcast: "127.0.0.1:9"}
	r, local := testRuntime(t, htpc)
	if err := r.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "htpc"}
	if err := r.Clone(ctx, domain.Template{}, l); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := New([]Node{htpc}, local.dial)
	if err := restarted.Persist(dir); err != nil {
		t.Fatal(err)
	}
	local.setOffline("htpc", true)
	o, err := restarted.Observe(ctx)
	if err != nil || o.Hosts[0].UnavailableReason != "asleep" || o.StorageContainers[0].Availability != "available" {
		t.Fatalf("restarted asleep node = %#v %#v %v", o.Hosts, o.StorageContainers, err)
	}
	if len(o.VMs) != 2 || o.VMs[0].State != "stopped" {
		t.Fatalf("golden not remembered: %#v", o.VMs)
	}
	if _, ok := vmNamed(o, l.VMName); !ok {
		t.Fatalf("lease forgotten across restart: %#v", o.VMs)
	}
}

// A remembered probe is never served as fresh: the first Observe after a
// restart dials the node.
func TestRememberedProbeIsReprobedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	r, local := testRuntime(t, mbp())
	if err := r.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := New([]Node{mbp()}, local.dial)
	if err := restarted.Persist(dir); err != nil {
		t.Fatal(err)
	}
	local.setOffline("mbp-2014", true)
	o, _ := restarted.Observe(ctx)
	if o.Hosts[0].UnavailableReason != "offline" || o.StorageContainers[0].Availability != "unavailable" || len(o.VMs) != 0 {
		t.Fatalf("stale probe served as fresh: %#v %#v", o.Hosts, o.VMs)
	}
}
