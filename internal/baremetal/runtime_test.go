package baremetal

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

const testHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOjnNwacnnaogoL+pe0tgXiGBNpMpHmcc7WSGhoYjssV"

// localNodes runs node commands with /bin/sh and a private HOME per node.
type localNodes struct {
	mu      sync.Mutex
	homes   map[string]string
	offline map[string]bool
	dials   int
	// path is prepended to PATH, for a fake podman; stubs holds a fake
	// caffeinate so host-lease tests never hold this Mac awake.
	path, stubs string
}

type localSession struct{ home, host, path, stubs string }

func (s localSession) Close() {}

func (s localSession) HostKey() string { return s.host }

func (s localSession) Dial(network, address string) (net.Conn, error) {
	return net.Dial(network, address)
}

func (s localSession) Run(ctx context.Context, command, input string) (string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	path := s.stubs + ":/usr/bin:/bin:/usr/sbin:/sbin"
	if s.path != "" {
		path = s.path + ":" + path
	}
	cmd.Env = []string{"HOME=" + s.home, "PATH=" + path}
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (l *localNodes) dial(_ context.Context, n Node) (session, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dials++
	if l.offline[n.ID] {
		return nil, errors.New("connect: no route to host")
	}
	return localSession{home: l.homes[n.ID], host: n.HostKeys[0], path: l.path, stubs: l.stubs}, nil
}

func (l *localNodes) setOffline(id string, offline bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.offline[id] = offline
}

func testRuntime(t *testing.T, nodes ...Node) (*Runtime, *localNodes) {
	t.Helper()
	local := &localNodes{homes: map[string]string{}, offline: map[string]bool{}, stubs: t.TempDir()}
	// A real caffeinate would outlive a test that never stops its lease.
	if err := os.WriteFile(filepath.Join(local.stubs, "caffeinate"), []byte("#!/bin/sh\nexec sleep 300\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		local.homes[n.ID] = t.TempDir()
	}
	r := New(nodes, local.dial)
	r.probeInterval = 0
	r.wakePoll = 10 * time.Millisecond
	r.wakeTimeout = 2 * time.Second
	return r, local
}

func mbp() Node {
	return Node{ID: "mbp-2014", Address: "127.0.0.1", User: "admin", HostKeys: []string{testHostKey}, OS: "macOS", Arch: "amd64"}
}

func vmNamed(o domain.Observation, name string) (domain.VM, bool) {
	for _, vm := range o.VMs {
		if vm.Name == name {
			return vm, true
		}
	}
	return domain.VM{}, false
}

func TestProbeReportsUITestingReadiness(t *testing.T) {
	base := "volume=Monterey\ncpu=4\nmemory=17179869184\n"
	for line, want := range map[string]string{
		"ui_testing=ready":                         "ready",
		"ui_testing=devtools_disabled":             "devtools_disabled",
		"ui_testing=automation_mode_requires_auth": "automation_mode_requires_auth",
		"ui_testing=no_console_session":            "no_console_session",
		"ui_testing=bogus":                         "",
		"":                                         "",
	} {
		p, err := parseProbe(base + line)
		if err != nil {
			t.Fatal(err)
		}
		if p.uiTesting != want {
			t.Fatalf("%q -> %q, want %q", line, p.uiTesting, want)
		}
	}
}

func TestNormalizeVolume(t *testing.T) {
	for in, want := range map[string]string{"Big Sur": "Big-Sur", "Macintosh HD - Data": "Macintosh-HD-Data", "bazzite": "bazzite", " Monterey ": "Monterey"} {
		if got := normalizeVolume(in); got != want {
			t.Fatalf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestLeaseLifecycleOnAReachableNode(t *testing.T) {
	r, local := testRuntime(t, mbp())
	ctx := context.Background()
	o, err := r.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Hosts) != 1 || o.Hosts[0].Availability != "available" || o.Hosts[0].Capacity.MaxVMs != 1 || o.StorageContainers[0].Availability != "available" {
		t.Fatalf("host = %#v", o.Hosts)
	}
	if len(o.VMs) != 1 || o.VMs[0].State != "stopped" || !domain.ValidName(o.VMs[0].Name) || o.VMs[0].Location != "mbp-2014" {
		t.Fatalf("pseudo-golden = %#v", o.VMs)
	}

	l := domain.Lease{ID: "lease-0123456789abcdef", VMName: "vf-0123456789abcdef", Location: "mbp-2014", SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC9D6mucoloj9cxdHV3tI03FWab3N3RsSl8Va/gXFvyQ"}
	if err := r.Clone(ctx, domain.Template{}, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	if vm, ok := vmNamed(o, l.VMName); !ok || vm.State != "stopped" {
		t.Fatalf("cloned lease = %#v", vm)
	}
	if err := r.Start(ctx, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	vm, ok := vmNamed(o, l.VMName)
	if !ok || vm.State != "running" || vm.IP != "127.0.0.1" || !vm.SSHAvailable || o.HostUsed != 1 || o.Hosts[0].Capacity.UsedVMs != 1 {
		t.Fatalf("started lease = %#v used=%d", vm, o.HostUsed)
	}
	connection, err := r.Prepare(ctx, l)
	if err != nil || connection.User != "admin" || connection.Port != 22 || connection.HostKey != testHostKey {
		t.Fatal(connection, err)
	}
	keys := filepath.Join(local.homes["mbp-2014"], ".ssh", "authorized_keys")
	if b, _ := os.ReadFile(keys); !strings.Contains(string(b), "virfield-lease-"+l.VMName) {
		t.Fatal("lease key not authorized")
	}
	if err := r.Stop(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	if _, ok := vmNamed(o, l.VMName); ok {
		t.Fatal("deleted lease still observed")
	}
	if b, _ := os.ReadFile(keys); strings.Contains(string(b), "virfield-lease-") {
		t.Fatal("lease key not revoked")
	}
}

func TestOfflineNodeIsUnavailableNotAnInventoryFailure(t *testing.T) {
	r, local := testRuntime(t, mbp())
	local.setOffline("mbp-2014", true)
	o, err := r.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o.Hosts[0].Availability != "unavailable" || o.Hosts[0].UnavailableReason != "offline" || o.StorageContainers[0].Availability != "unavailable" || len(o.VMs) != 0 {
		t.Fatalf("offline node = %#v %#v %#v", o.Hosts, o.StorageContainers, o.VMs)
	}
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "mbp-2014"}
	var failure *domain.Error
	if err := r.Start(context.Background(), l); !errors.As(err, &failure) || failure.Code != "backend_unavailable" {
		t.Fatal(err)
	}
}

func TestAsleepNodeIsAdmittedAndWokenForItsLease(t *testing.T) {
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	htpc := Node{ID: "htpc", Address: "127.0.0.1", User: "bazzite", HostKeys: []string{testHostKey}, OS: "linux", Arch: "amd64", WakeMAC: "B0:6E:BF:60:6D:64", WakeBroadcast: listener.LocalAddr().String()}
	r, local := testRuntime(t, htpc)
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	local.setOffline("htpc", true)
	o, _ := r.Observe(ctx)
	if o.Hosts[0].UnavailableReason != "asleep" || o.StorageContainers[0].Availability != "available" || len(o.VMs) != 1 || o.VMs[0].State != "stopped" {
		t.Fatalf("asleep node = %#v %#v %#v", o.Hosts, o.StorageContainers, o.VMs)
	}
	// The magic packet brings the node back.
	go func() {
		buffer := make([]byte, 200)
		if n, _, err := listener.ReadFrom(buffer); err == nil && n == 102 {
			local.setOffline("htpc", false)
		}
	}()
	local.setOffline("htpc", true)
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "htpc"}
	// The controller clones before it starts: clone must wake the node.
	if err := r.Clone(ctx, domain.Template{}, l); err != nil {
		t.Fatal("clone did not wake the node", err)
	}
	if err := r.Start(ctx, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	if vm, ok := vmNamed(o, l.VMName); !ok || vm.State != "running" {
		t.Fatalf("woken lease = %#v", vm)
	}
	_ = r.Delete(ctx, l)
}

// An unreachable node is re-probed at most every offline backoff, so a
// sleeping node cannot stretch every inventory refresh by its dial timeout.
func TestOfflineNodeIsReprobedOnlyAfterBackoff(t *testing.T) {
	r, local := testRuntime(t, mbp())
	r.offlineBackoff = time.Hour
	local.setOffline("mbp-2014", true)
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	dials := local.dials
	o, err := r.Observe(ctx)
	if err != nil || local.dials != dials || o.Hosts[0].UnavailableReason != "offline" {
		t.Fatal("offline node re-probed inside its backoff", local.dials, dials, o.Hosts, err)
	}
	r.offlineBackoff = 0
	local.setOffline("mbp-2014", false)
	o, _ = r.Observe(ctx)
	if o.Hosts[0].Availability != "available" {
		t.Fatal("node not re-probed after its backoff")
	}
}

// Inventory must not keep a node awake: an online node is probed at most once
// per probe interval, unless a Virfield operation just changed it.
func TestOnlineNodeIsProbedOncePerIntervalUnlessChanged(t *testing.T) {
	r, local := testRuntime(t, mbp())
	r.probeInterval = time.Hour
	ctx := context.Background()
	if _, err := r.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	dials := local.dials
	o, err := r.Observe(ctx)
	if err != nil || local.dials != dials || o.Hosts[0].Availability != "available" || len(o.VMs) != 1 {
		t.Fatal("online node re-probed inside its interval", local.dials, dials, o.Hosts, err)
	}
	l := domain.Lease{VMName: "vf-0123456789abcdef", Location: "mbp-2014"}
	if err := r.Clone(ctx, domain.Template{}, l); err != nil {
		t.Fatal(err)
	}
	o, _ = r.Observe(ctx)
	if _, ok := vmNamed(o, l.VMName); !ok {
		t.Fatal("a changed node was not re-probed")
	}
	_ = r.Delete(ctx, l)
}
