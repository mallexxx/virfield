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
	node := Node{ID: "linux-node", Address: "127.0.0.1", User: "operator", HostKeys: []string{testHostKey}, OS: "linux", Arch: "amd64", WakeMAC: "02:00:00:00:00:01", WakeBroadcast: "127.0.0.1:9"}
	r, local := testRuntime(t, node)
	if err := r.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "linux-node"}
	if err := r.Clone(ctx, domain.Template{}, l); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := New([]Node{node}, local.dial)
	if err := restarted.Persist(dir); err != nil {
		t.Fatal(err)
	}
	local.setOffline("linux-node", true)
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
	local.setOffline("mac-node", true)
	o, _ := restarted.Observe(ctx)
	if o.Hosts[0].UnavailableReason != "offline" || o.StorageContainers[0].Availability != "unavailable" || len(o.VMs) != 0 {
		t.Fatalf("stale probe served as fresh: %#v %#v", o.Hosts, o.VMs)
	}
}
