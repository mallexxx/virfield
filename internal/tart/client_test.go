package tart

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
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
