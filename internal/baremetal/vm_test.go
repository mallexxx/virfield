package baremetal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type blockedCloneSession struct {
	session
	entered chan<- struct{}
	proceed <-chan struct{}
}

func (s blockedCloneSession) Run(ctx context.Context, cmd, input string) (string, error) {
	if strings.Contains(cmd, "no free VM port") {
		close(s.entered)
		select {
		case <-s.proceed:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return s.session.Run(ctx, cmd, input)
}

func TestObserveDuringCloneCannotCacheAbsentVMForNextObservation(t *testing.T) {
	r, local, _ := vmRuntime(t, "secured")
	r.probeInterval = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	entered, proceed := make(chan struct{}), make(chan struct{})
	r.dial = func(ctx context.Context, n Node) (session, error) {
		s, err := local.dial(ctx, n)
		return blockedCloneSession{session: s, entered: entered, proceed: proceed}, err
	}
	l := domain.Lease{VMName: "vf-observed-during-clone", Location: "htpc", ImageID: "image-ready"}
	completed := make(chan error, 1)
	go func() { completed <- r.Clone(ctx, domain.Template{Name: "secured"}, l) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("clone did not enter the mutation")
	}
	o, err := r.Observe(ctx)
	if _, exists := vmNamed(o, l.VMName); err != nil || exists {
		t.Fatal("unexpected observation before clone completion", o, err)
	}
	close(proceed)
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	o, err = r.Observe(ctx)
	if vm, exists := vmNamed(o, l.VMName); err != nil || !exists || vm.State != "stopped" {
		t.Fatal("completed clone remained absent in cached inventory", o.VMs, err)
	}
}

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
	r, local, _ := vmRuntime(t, "htpc-sonoma-14")
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
	if vm, _ := vmNamed(o, l.VMName); vm.State != "running" || vm.SSHAvailable {
		t.Fatalf("booting guest reported SSH = %#v", vm)
	}
	if err := os.WriteFile(filepath.Join(local.homes["htpc"], "sshd"), []byte("22100\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r.reached(r.nodes[0])
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

type fakeGuests struct{ prepared, forgotten []string }

func (g *fakeGuests) Prepare(_ context.Context, l domain.Lease) (domain.SSHConnection, error) {
	g.prepared = append(g.prepared, l.VMName)
	return domain.SSHConnection{User: "admin", Port: l.SSHPort, HostKey: testHostKey}, nil
}

func (g *fakeGuests) LeasePassword(l domain.Lease) (string, error) { return "pw-" + l.VMName, nil }

func (g *fakeGuests) Forget(l domain.Lease) error {
	g.forgotten = append(g.forgotten, l.VMName)
	return nil
}

func TestPrepareDelegatesVMLeasesToGuests(t *testing.T) {
	r, _, _ := vmRuntime(t, "htpc-sonoma-14")
	g := &fakeGuests{}
	r.SetGuests(g)
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "htpc", ImageID: "image-0123", SSHPort: 22100, SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC9D6mucoloj9cxdHV3tI03FWab3N3RsSl8Va/gXFvyQ"}
	c, err := r.Prepare(context.Background(), l)
	if err != nil || c.Port != 22100 || len(g.prepared) != 1 {
		t.Fatal(c, err, g.prepared)
	}
	if err := r.Forget(l); err != nil || len(g.forgotten) != 1 {
		t.Fatal(err, g.forgotten)
	}
	if pw, err := r.LeasePassword(l); err != nil || pw != "pw-"+l.VMName {
		t.Fatal(pw, err)
	}
	if _, err := r.LeasePassword(domain.Lease{VMName: "vf-fedcba9876543210", Location: "htpc"}); err == nil {
		t.Fatal("host lease has a guest password")
	}
	host := domain.Lease{VMName: "vf-fedcba9876543210", Location: "htpc"}
	if err := r.Forget(host); err != nil || len(g.forgotten) != 1 {
		t.Fatal("host lease reached the guests manager")
	}
}

func TestNodeImageVMLifecycle(t *testing.T) {
	r, local, goldens := vmRuntime(t, "sonoma-bootstrap")
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{ID: "image-0123", VMName: "htpc-sonoma", Location: "htpc", Purpose: "image"}
	if err := r.CloneImage(ctx, l, "sonoma-bootstrap"); err != nil {
		t.Fatal(err)
	}
	if err := r.StartImage(ctx, l); err != nil {
		t.Fatal(err)
	}
	o, _ := r.Observe(ctx)
	vm, ok := vmNamed(o, "htpc-sonoma")
	if !ok || vm.State != "running" || vm.SSHPort != 22102 {
		t.Fatalf("image VM = %#v", vm)
	}
	calls := read(t, filepath.Join(local.path, "podman.log"))
	if !strings.Contains(calls, "-p 127.0.0.1:22102:22") || strings.Contains(calls, ":5900") {
		t.Fatal("image VM published beyond the node loopback or with a desktop:", calls)
	}
	if err := r.StopImage(ctx, l); err != nil {
		t.Fatal(err)
	}
	// The fake qemu-img writes nothing; stand in for the overlay.
	disk := filepath.Join(local.homes["htpc"], "virfield", "images", "htpc-sonoma", "storage", "data.qcow2")
	if err := os.WriteFile(disk, []byte("overlay"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(goldens, 0755); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitImage(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(goldens, "htpc-sonoma.opencore.plist")); err != nil {
		t.Fatal("commit did not publish the golden's OpenCore config", err)
	}
	for _, bad := range []string{"vf-0123456789abcdef", "../x"} {
		if err := r.CloneImage(ctx, domain.Lease{VMName: bad, Location: "htpc"}, "sonoma-bootstrap"); err == nil {
			t.Fatal("accepted image name", bad)
		}
	}
}

// A golden committed after the last probe must still be cloned as a VM:
// a verified image never falls back to the host-lease path.
func TestNodeVMLeaseIgnoresAStaleProbe(t *testing.T) {
	r, local, goldens := vmRuntime(t)
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"htpc-sequoia-15.qcow2", "htpc-sequoia-15.opencore.plist"} {
		if err := os.WriteFile(filepath.Join(goldens, f), nil, 0444); err != nil {
			t.Fatal(err)
		}
	}
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "htpc", ImageID: "image-0123"}
	if err := r.Clone(ctx, domain.Template{Name: "htpc-sequoia-15"}, l); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(local.homes["htpc"], "virfield", "leases", l.VMName)
	if read(t, filepath.Join(dir, "kind")) != "vm" {
		t.Fatal("verified image cloned as a host lease")
	}
	if err := r.Start(ctx, l); err != nil {
		t.Fatal("start before a probe:", err)
	}
	if !strings.Contains(read(t, filepath.Join(local.path, "podman.log")), "run -d --name virfield-"+l.VMName) {
		t.Fatal("start did not run the VM")
	}
	if err := r.Delete(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("delete kept the clone")
	}
	missing := domain.Lease{VMName: "vf-fedcba9876543210", Location: "htpc", ImageID: "image-0123"}
	err := r.Clone(ctx, domain.Template{Name: "absent"}, missing)
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != "template_unavailable" {
		t.Fatal(err)
	}
}
