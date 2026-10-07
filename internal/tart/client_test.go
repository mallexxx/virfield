package tart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

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

func TestObserveKeepsUnmountedExternalContainer(t *testing.T) {
	runner := &fakeRunner{responses: map[string]string{
		"/local|list --source local --format json": `[{"Name":"golden","Running":false,"State":"stopped"}]`,
		"/local|get golden --format json":          `{"OS":"darwin","CPU":4,"Memory":8192,"Disk":80,"Running":false,"State":"stopped"}`,
	}}
	client := &Client{
		config: Config{HostID: "macbook", HostName: "MacBook", MaxVMs: 2, Containers: []ContainerConfig{
			{ID: "local", Path: "/local", Kind: "local"},
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
	runner := &fakeRunner{responses: map[string]string{"/samsung|clone ghcr.io/cirrus/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa vf-test": ""}}
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
	mu    sync.Mutex
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
		config: Config{HostID: "macbook", MaxVMs: 2, Containers: []ContainerConfig{{ID: "local", Path: "/local", Kind: "local"}}},
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
