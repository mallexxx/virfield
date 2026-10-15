package tart

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestCLIOutputDrainIsBoundedWhenAChildKeepsPipesOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tart-test")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 5 &\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &execRunner{binary: path}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := r.Run(ctx, t.TempDir(), []string{"list", "--format", "json"})
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatal("Inherited output pipes were not bounded", err)
	}
}

type fakeRunner struct {
	responses map[string]string
	calls     []string
}

func (r *fakeRunner) Run(_ context.Context, home string, args []string) ([]byte, error) {
	key := home + "|" + strings.Join(args, " ")
	r.calls = append(r.calls, key)
	response, ok := r.responses[key]
	if !ok {
		return nil, errors.New("unexpected command: " + key)
	}
	return []byte(response), nil
}

func (r *fakeRunner) StartDetached(home string, args []string) error {
	r.calls = append(r.calls, home+"|"+strings.Join(args, " "))
	return nil
}

// Tart can report zero/absent Disk while ASIF is held by a running builder.
// config.json does not change when the stopped disk becomes readable again.
func TestIncompleteDiskSpecIsRefreshedWithoutConfigChange(t *testing.T) {
	for _, incomplete := range []string{`"Disk":0`, `"Other":0`} {
		t.Run(incomplete, func(t *testing.T) {
			home := tartHome(t, "golden")
			key := home + "|get golden --format json"
			runner := &fakeRunner{responses: map[string]string{key: `{"OS":"darwin","CPU":4,"Memory":8192,` + incomplete + `}`}}
			client := &Client{runner: runner}
			container := ContainerConfig{ID: "local", Path: home}
			first, err := client.spec(context.Background(), container, "golden")
			if err != nil || first.disk != 0 {
				t.Fatal("Incomplete disk did not remain unknown", err)
			}
			runner.responses[key] = `{"OS":"darwin","CPU":4,"Memory":8192,"Disk":140}`
			second, err := client.spec(context.Background(), container, "golden")
			if err != nil || second.disk != 140_000_000_000 || len(runner.calls) != 2 {
				t.Fatal("Incomplete ASIF size poisoned the unchanged-config cache", second.disk, len(runner.calls), err)
			}
			third, err := client.spec(context.Background(), container, "golden")
			if err != nil || third.disk != second.disk || len(runner.calls) != 2 {
				t.Fatal("Verified disk size was not cached", third.disk, len(runner.calls), err)
			}
		})
	}
}

type interruptedIPRunner struct{ cancel context.CancelFunc }

func (r interruptedIPRunner) Run(ctx context.Context, _ string, args []string) ([]byte, error) {
	if args[0] == "get" {
		return []byte(`{"OS":"darwin","CPU":4,"Memory":8192,"Disk":80}`), nil
	}
	r.cancel()
	return nil, ctx.Err()
}

func (interruptedIPRunner) StartDetached(string, []string) error { return nil }

func TestInspectRejectsInterruptedAddressDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{runner: interruptedIPRunner{cancel: cancel}}
	_, err := client.inspect(ctx, ContainerConfig{ID: "local", Path: t.TempDir()}, listRow{Name: "vm", State: "running"}, time.Now())
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Interrupted address discovery was reported as a healthy VM with no address", err)
	}
}

func TestObserveRejectsInterruptedContainerProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := tartHome(t)
	client := &Client{
		config: Config{HostID: "macbook", MaxVMs: 2, Containers: []ContainerConfig{
			{ID: "local", Path: local, Kind: "local"},
			{ID: "external", Path: "/external", Kind: "external"},
		}},
		probe: func(ctx context.Context, path string) (volume, error) {
			if path == "/external" {
				cancel()
				return volume{}, ctx.Err()
			}
			return volume{Identity: "local", Total: 1 << 40, Free: 500 << 30}, nil
		},
		now: time.Now,
	}
	o, err := client.Observe(ctx)
	if !errors.Is(err, context.Canceled) || len(o.Hosts) != 0 || len(o.StorageContainers) != 0 {
		t.Fatal("Interrupted refresh returned a partial healthy inventory", o, err)
	}
}

// tartHome creates a TART_HOME with one stopped VM directory per name.
func tartHome(t *testing.T, names ...string) string {
	t.Helper()
	home := t.TempDir()
	for _, name := range names {
		dir := filepath.Join(home, "vms", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":1}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestObserveKeepsUnmountedExternalContainer(t *testing.T) {
	local := tartHome(t, "golden")
	runner := &fakeRunner{responses: map[string]string{
		local + "|get golden --format json": `{"OS":"darwin","CPU":4,"Memory":8192,"Disk":80,"Running":false,"State":"stopped"}`,
	}}
	client := &Client{
		config: Config{HostID: "macbook", HostName: "MacBook", MaxVMs: 2, Containers: []ContainerConfig{
			{ID: "local", Path: local, Kind: "local"},
			{ID: "samsung", Path: "/samsung", Kind: "external", VolumeUUID: "123e4567-e89b-12d3-a456-426614174000"},
		}},
		runner: runner,
		probe: func(_ context.Context, path string) (volume, error) {
			if path == "/samsung" {
				return volume{}, os.ErrNotExist
			}
			return volume{Identity: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Total: 1 << 40, Free: 500 << 30}, nil
		},
		now:      func() time.Time { return time.Unix(100, 0).UTC() },
		capacity: domain.HostCapacity{Total: domain.Resources{CPU: 8, MemoryBytes: 16 << 30}, Allocatable: domain.Resources{CPU: 8, MemoryBytes: 12 << 30}, MemoryReserveBytes: 4 << 30, MaxVMs: 2},
	}

	observation, err := client.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.StorageContainers) != 2 || observation.StorageContainers[1].ID != "samsung" || observation.StorageContainers[1].Availability != "unavailable" || observation.StorageContainers[1].UnavailableReason != "volume_unmounted" {
		t.Fatalf("external container disappeared: %#v", observation.StorageContainers)
	}
	if len(observation.VMs) != 1 || observation.VMs[0].ContainerID != "local" || observation.VMs[0].OS != "macOS" {
		t.Fatal(observation.VMs)
	}
	if got := observation.Hosts[0].Capacity.Available.MemoryBytes; got != 12<<30 {
		t.Fatalf("available memory = %d", got)
	}
}

func TestCommandsUseSelectedTartHome(t *testing.T) {
	runner := &fakeRunner{responses: map[string]string{"/samsung|clone ghcr.io/cirrus/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa vf-test": "", "/samsung|set vf-test --display 1920x1080px --no-display-refit": ""}}
	client := &Client{config: Config{Containers: []ContainerConfig{{ID: "samsung", Path: "/samsung", Kind: "external"}}}, runner: runner}
	template := domain.Template{Source: "ghcr.io/cirrus/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	lease := domain.Lease{VMName: "vf-test", Location: "samsung"}
	if err := client.Clone(context.Background(), template, lease); err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/samsung|clone ghcr.io/cirrus/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa vf-test",
		"/samsung|set vf-test --display 1920x1080px --no-display-refit",
		"/samsung|run --no-graphics vf-test",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestDiskSizeUsesAPFSContainerValuesWhenVolumeValuesAreZero(t *testing.T) {
	values := map[string]string{
		"TotalSize":         "499898105856",
		"FreeSpace":         "0",
		"APFSContainerSize": "499898105856",
		"APFSContainerFree": "390241533952",
	}
	total, err := diskSize(values, "TotalSize", "APFSContainerSize")
	if err != nil || total != 499898105856 {
		t.Fatalf("total = %d, err = %v", total, err)
	}
	free, err := diskSize(values, "FreeSpace", "APFSContainerFree")
	if err != nil || free != 390241533952 {
		t.Fatalf("free = %d, err = %v", free, err)
	}
}

func TestRegistryCredentialOnlyAppliesToMatchingRemote(t *testing.T) {
	runner := &execRunner{credentials: map[string]RegistryCredential{
		"ghcr.io": {Host: "ghcr.io", Username: "operator", Token: "secret-token"},
	}}
	remote := runner.environment("/storage", []string{"clone", "ghcr.io/cirruslabs/image@sha256:abc", "vm"})
	joined := strings.Join(remote, "\n")
	for _, expected := range []string{"TART_HOME=/storage", "TART_REGISTRY_HOSTNAME=ghcr.io", "TART_REGISTRY_USERNAME=operator", "TART_REGISTRY_PASSWORD=secret-token"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("environment missing %q", expected)
		}
	}
	local := strings.Join(runner.environment("/storage", []string{"list", "--source", "local"}), "\n")
	if strings.Contains(local, "TART_REGISTRY_PASSWORD") {
		t.Fatal("registry secret leaked into a local-only Tart command")
	}
}

func TestMountPointResolvesTheVolumeOfANestedDirectory(t *testing.T) {
	root, err := mountPoint("/")
	if err != nil || root != "/" {
		t.Fatal(root, err)
	}
	nested := t.TempDir()
	mount, err := mountPoint(nested)
	if err != nil || mount == "" || mount[0] != '/' || mount == nested {
		t.Fatalf("mount point of %s = %q, %v", nested, mount, err)
	}
}

// tart run exits at once when the VM is still held (for example by a
// just-finished Packer build); that must not be reported as a start.
func TestStartDetachedReportsImmediateExit(t *testing.T) {
	dir := t.TempDir()
	failing := filepath.Join(dir, "tart-fails")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\necho 'VM \"x\" is already running!' >&2\nexit 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &execRunner{binary: failing}
	if err := r.StartDetached(dir, []string{"run", "x"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatal("immediate exit reported as a start", err)
	}
	running := filepath.Join(dir, "tart-runs")
	if err := os.WriteFile(running, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r = &execRunner{binary: running}
	if err := r.StartDetached(dir, []string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
}

type slowGetRunner struct {
	names []string
	delay time.Duration
}

func (r *slowGetRunner) Run(_ context.Context, _ string, args []string) ([]byte, error) {
	switch args[0] {
	case "list":
		rows := make([]string, 0, len(r.names))
		for _, name := range r.names {
			rows = append(rows, `{"Name":"`+name+`","Running":false,"State":"stopped"}`)
		}
		return []byte("[" + strings.Join(rows, ",") + "]"), nil
	case "get":
		// ASIF disks make tart get compute allocated size (~1 s per VM).
		time.Sleep(r.delay)
		return []byte(`{"OS":"darwin","CPU":4,"Memory":8192,"Disk":80,"Running":false,"State":"stopped"}`), nil
	}
	return nil, errors.New("unexpected command")
}

func (r *slowGetRunner) StartDetached(string, []string) error { return nil }

func TestObserveInspectsVMsConcurrentlyInListOrder(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f"}
	client := &Client{
		config: Config{HostID: "macbook", MaxVMs: 2, Containers: []ContainerConfig{{ID: "local", Path: tartHome(t, names...), Kind: "local"}}},
		runner: &slowGetRunner{names: names, delay: 200 * time.Millisecond},
		probe: func(context.Context, string) (volume, error) {
			return volume{Identity: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Total: 1 << 40, Free: 500 << 30}, nil
		},
		now: func() time.Time { return time.Unix(100, 0).UTC() },
	}
	started := time.Now()
	observation, err := client.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 800*time.Millisecond {
		t.Fatalf("inventory of %d VMs took %v; tart get calls ran sequentially", len(names), elapsed)
	}
	for i, vm := range observation.VMs {
		if vm.Name != names[i] {
			t.Fatalf("VM order changed: %v", observation.VMs)
		}
	}
}

type countingRunner struct {
	mu    sync.Mutex
	gets  int
	calls int
}

func (r *countingRunner) Run(_ context.Context, _ string, args []string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	switch args[0] {
	case "list":
		return []byte(`[{"Name":"golden","Running":false,"State":"stopped"}]`), nil
	case "get":
		r.gets++
		return []byte(`{"OS":"darwin","CPU":4,"Memory":8192,"Disk":80,"Running":false,"State":"stopped"}`), nil
	}
	return nil, errors.New("unexpected command")
}

func (r *countingRunner) StartDetached(string, []string) error { return nil }

// tart get on an ASIF VM attaches and mounts its disk image to size it, and
// Finder then crawls the mounted macOS volume. Specs are cached until the VM
// configuration changes; live state comes from tart list.
func TestObserveReusesVMSpecsUntilConfigChanges(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "vms", "golden", "config.json")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &countingRunner{}
	client := &Client{
		config: Config{HostID: "macbook", MaxVMs: 2, Containers: []ContainerConfig{{ID: "local", Path: home, Kind: "local"}}},
		runner: runner,
		probe: func(context.Context, string) (volume, error) {
			return volume{Identity: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Total: 1 << 40, Free: 500 << 30}, nil
		},
		now: func() time.Time { return time.Unix(100, 0).UTC() },
	}
	for range 3 {
		o, err := client.Observe(context.Background())
		if err != nil || len(o.VMs) != 1 || o.VMs[0].Resources.CPU != 4 {
			t.Fatal(o.VMs, err)
		}
	}
	if runner.gets != 1 {
		t.Fatalf("tart get ran %d times for an unchanged VM", runner.gets)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(config, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Observe(context.Background()); err != nil || runner.gets != 2 {
		t.Fatal("changed VM configuration was not re-read", runner.gets, err)
	}
}

// A running Tart VM is one whose config.json is locked by its tart run
// process; listing must not run tart list, which mounts every ASIF disk.
func TestListReadsVMDirectoriesAndRunLocks(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"golden", "vf-lease"} {
		dir := filepath.Join(home, "vms", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":1}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "vms", "incomplete"), 0700); err != nil {
		t.Fatal(err)
	}
	// Another process holds the run lock, as tart run does.
	holder := exec.Command("/usr/bin/python3", "-c", "import fcntl,sys,time\nf=open(sys.argv[1],'r+')\nfcntl.lockf(f,fcntl.LOCK_EX)\nprint('locked',flush=True)\ntime.sleep(30)", filepath.Join(home, "vms", "vf-lease", "config.json"))
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()
	ready := make([]byte, 7)
	if _, err := io.ReadFull(out, ready); err != nil {
		t.Fatal(err)
	}
	runner := &countingRunner{}
	client := &Client{runner: runner}
	rows, err := client.list(context.Background(), ContainerConfig{ID: "local", Path: home})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]bool{}
	for _, row := range rows {
		states[row.Name] = row.Running
	}
	if len(rows) != 2 || states["golden"] || !states["vf-lease"] {
		t.Fatalf("rows = %#v", rows)
	}
	if runner.calls != 0 {
		t.Fatal("listing ran a tart command")
	}
}
