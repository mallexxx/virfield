package baremetal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func vmNode(goldens string) Node {
	return Node{ID: "htpc", Address: "127.0.0.1", User: "bazzite", HostKeys: []string{testHostKey}, OS: "linux", Arch: "amd64",
		VMs: &VMConfig{Image: testVMImage, GoldensDir: goldens, DiskSize: "100G", PortBase: 22100, Max: 2, CPU: 4, MemoryGB: 8}}
}

func vmRuntime(t *testing.T, goldens ...string) (*Runtime, *localNodes, string) {
	t.Helper()
	dir := goldensDir(t, goldens...)
	r, local := testRuntime(t, vmNode(dir))
	local.path, _ = fakePodman(t)
	return r, local, dir
}

func TestNodeVMGoldensAreObserved(t *testing.T) {
	r, _, _ := vmRuntime(t, "htpc-sonoma-14")
	o, err := r.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	vm, ok := vmNamed(o, "htpc-sonoma-14")
	if !ok || vm.State != "stopped" || vm.Location != "htpc" || vm.OS != "macOS" || vm.Provider != providerName {
		t.Fatalf("golden = %#v", o.VMs)
	}
	if o.Hosts[0].Capacity.MaxVMs != 3 {
		t.Fatal("capacity", o.Hosts[0].Capacity)
	}
}

func TestNodeVMLeaseLifecycle(t *testing.T) {
	r, _, _ := vmRuntime(t, "htpc-sonoma-14")
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{ID: "lease-0123456789abcdef", VMName: "vf-0123456789abcdef", Location: "htpc", ImageID: "image-0123"}
	if err := r.Clone(ctx, domain.Template{Name: "htpc-sonoma-14"}, l); err != nil {
		t.Fatal(err)
	}
	o, _ := r.Observe(ctx)
	if vm, ok := vmNamed(o, l.VMName); !ok || vm.State != "stopped" || vm.SSHPort != 22100 {
		t.Fatalf("clone = %#v", vm)
	}
	if err := r.Start(ctx, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	vm, _ := vmNamed(o, l.VMName)
	if vm.State != "running" || vm.IP != "127.0.0.1" || vm.SSHPort != 22100 || !vm.SSHAvailable || vm.OS != "macOS" || o.Hosts[0].Capacity.UsedVMs != 1 {
		t.Fatalf("started = %#v used=%d", vm, o.Hosts[0].Capacity.UsedVMs)
	}
	if err := r.Stop(ctx, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	if vm, _ := vmNamed(o, l.VMName); vm.State != "stopped" {
		t.Fatalf("stopped = %#v", vm)
	}
	if err := r.Delete(ctx, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	if _, ok := vmNamed(o, l.VMName); ok {
		t.Fatal("deleted clone still observed")
	}
}

func TestNodeVMCloneRequiresAVerifiedImage(t *testing.T) {
	r, local, _ := vmRuntime(t, "sonoma-bootstrap")
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "htpc"}
	err := r.Clone(ctx, domain.Template{Name: "sonoma-bootstrap"}, l)
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != "invalid_request" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(local.homes["htpc"], "virfield", "leases", l.VMName)); !os.IsNotExist(err) {
		t.Fatal("refused clone touched the node")
	}
}

func TestNodeVMStopFallsBackWhenTheProbeIsStale(t *testing.T) {
	r, _, _ := vmRuntime(t, "htpc-sonoma-14")
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "htpc", ImageID: "image-0123"}
	if err := r.Clone(ctx, domain.Template{Name: "htpc-sonoma-14"}, l); err != nil {
		t.Fatal(err)
	}
	// No Observe since the clone: the runtime does not know it is a VM yet.
	if err := r.Delete(ctx, l); err != nil {
		t.Fatal("delete of an unobserved VM clone failed", err)
	}
	o, _ := r.Observe(ctx)
	if _, ok := vmNamed(o, l.VMName); ok {
		t.Fatal("clone survived delete")
	}
}

func TestHostLeaseStillWorksBesideVMs(t *testing.T) {
	r, _, _ := vmRuntime(t, "htpc-sonoma-14")
	ctx := context.Background()
	o, err := r.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pseudo := ""
	for _, vm := range o.VMs {
		if vm.Name != "htpc-sonoma-14" {
			pseudo = vm.Name
		}
	}
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "htpc"}
	if err := r.Clone(ctx, domain.Template{Name: pseudo}, l); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	if vm, _ := vmNamed(o, l.VMName); vm.State != "running" || vm.SSHPort != 0 {
		t.Fatalf("host lease = %#v", vm)
	}
	if err := r.Delete(ctx, l); err != nil {
		t.Fatal(err)
	}
}

func TestNodeVMsSurviveRestartWhileNodeSleeps(t *testing.T) {
	r, local, goldens := vmRuntime(t, "htpc-sonoma-14")
	n := vmNode(goldens)
	n.WakeMAC, n.WakeBroadcast = "B0:6E:BF:60:6D:64", "127.0.0.1:9"
	r.nodes = []Node{n}
	dir := t.TempDir()
	if err := r.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "htpc", ImageID: "image-0123"}
	if err := r.Clone(ctx, domain.Template{Name: "htpc-sonoma-14"}, l); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := New([]Node{n}, local.dial)
	if err := restarted.Persist(dir); err != nil {
		t.Fatal(err)
	}
	local.setOffline("htpc", true)
	o, _ := restarted.Observe(ctx)
	if _, ok := vmNamed(o, "htpc-sonoma-14"); !ok {
		t.Fatalf("golden forgotten: %#v", o.VMs)
	}
	if vm, ok := vmNamed(o, l.VMName); !ok || vm.SSHPort != 22100 {
		t.Fatalf("VM lease forgotten: %#v", o.VMs)
	}
}
