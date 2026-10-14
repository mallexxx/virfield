package fleet

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/provider"
)

type fakeRuntime struct {
	name  string
	obs   domain.Observation
	err   error
	calls []string
	woken []string
}

func (f *fakeRuntime) ProviderName() string { return f.name }
func (f *fakeRuntime) Observe(context.Context) (domain.Observation, error) {
	return f.obs, f.err
}
func (f *fakeRuntime) Clone(_ context.Context, _ domain.Template, l domain.Lease) error {
	f.calls = append(f.calls, "clone "+l.VMName)
	return nil
}
func (f *fakeRuntime) Start(_ context.Context, l domain.Lease) error {
	f.calls = append(f.calls, "start "+l.VMName)
	return nil
}
func (f *fakeRuntime) Stop(_ context.Context, l domain.Lease) error {
	f.calls = append(f.calls, "stop "+l.VMName)
	return nil
}
func (f *fakeRuntime) Delete(_ context.Context, l domain.Lease) error {
	f.calls = append(f.calls, "delete "+l.VMName)
	return nil
}
func (f *fakeRuntime) WakeHost(_ context.Context, id string) error {
	f.woken = append(f.woken, id)
	return nil
}

func observation(provider, host string, used, max int, vms ...string) domain.Observation {
	o := domain.Observation{Provider: provider, HostUsed: used, HostMax: max, At: time.Unix(100, 0), Hosts: []domain.Host{{ID: host}}, StorageContainers: []domain.StorageContainer{{ID: host, HostID: host}}}
	for _, vm := range vms {
		o.VMs = append(o.VMs, domain.VM{Name: vm, Location: host})
	}
	return o
}

func TestObserveMergesInventoriesUnderThePrimaryName(t *testing.T) {
	tart := &fakeRuntime{name: "tart", obs: observation("tart", "local", 1, 2, "golden")}
	nodes := &fakeRuntime{name: "baremetal", obs: observation("baremetal", "mbp-2014", 1, 2, "Monterey", "vf-1")}
	f := New(tart, nodes)
	o, err := f.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.ProviderName() != "tart" || o.Provider != "tart" || len(o.VMs) != 3 || len(o.Hosts) != 2 || len(o.StorageContainers) != 2 || o.HostUsed != 2 || o.HostMax != 4 {
		t.Fatalf("merged = %#v", o)
	}
	tart.err = errors.New("tart down")
	if _, err := f.Observe(context.Background()); err == nil {
		t.Fatal("a failed runtime must pause admission")
	}
}

func TestLifecycleAndWakeRouteToTheOwningRuntime(t *testing.T) {
	tart := &fakeRuntime{name: "tart", obs: observation("tart", "local", 0, 2)}
	nodes := &fakeRuntime{name: "baremetal", obs: observation("baremetal", "htpc", 0, 1)}
	f := New(tart, nodes)
	ctx := context.Background()
	if _, err := f.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	vm := domain.Lease{VMName: "vf-vm", Provider: "tart"}
	node := domain.Lease{VMName: "vf-node", Provider: "baremetal"}
	legacy := domain.Lease{VMName: "vf-legacy"}
	_ = f.Clone(ctx, domain.Template{Provider: "baremetal"}, node)
	_ = f.Clone(ctx, domain.Template{}, vm)
	for _, l := range []domain.Lease{vm, node, legacy} {
		_ = f.Start(ctx, l)
		_ = f.Stop(ctx, l)
		_ = f.Delete(ctx, l)
	}
	if len(nodes.calls) != 4 || nodes.calls[0] != "clone vf-node" || len(tart.calls) != 7 {
		t.Fatalf("tart=%v nodes=%v", tart.calls, nodes.calls)
	}
	if err := f.Start(ctx, domain.Lease{Provider: "orchard"}); err == nil {
		t.Fatal("routed to an unknown provider")
	}
	if err := f.WakeHost(ctx, "htpc"); err != nil || len(nodes.woken) != 1 || len(tart.woken) != 0 {
		t.Fatal(err, nodes.woken, tart.woken)
	}
}

type fakePreparer struct{ name string }

func (p fakePreparer) Prepare(context.Context, domain.Lease) (domain.SSHConnection, error) {
	return domain.SSHConnection{User: p.name}, nil
}

func TestPreparerRoutesByLeaseProvider(t *testing.T) {
	p := NewPreparer(fakePreparer{"vm"}, map[string]Preparer{"baremetal": fakePreparer{"node"}})
	for provider, want := range map[string]string{"": "vm", "tart": "vm", "baremetal": "node"} {
		c, err := p.Prepare(context.Background(), domain.Lease{Provider: provider})
		if err != nil || c.User != want {
			t.Fatalf("%s -> %s %v", provider, c.User, err)
		}
	}
}

type slowRuntime struct{ fakeRuntime }

func (s *slowRuntime) Observe(ctx context.Context) (domain.Observation, error) {
	time.Sleep(300 * time.Millisecond)
	return s.fakeRuntime.Observe(ctx)
}

// A refresh must cost the slowest runtime, not the sum: the controller treats
// inventory older than ten seconds as stale.
func TestObserveRunsRuntimesConcurrently(t *testing.T) {
	a := &slowRuntime{fakeRuntime{name: "tart", obs: observation("tart", "local", 0, 2)}}
	b := &slowRuntime{fakeRuntime{name: "baremetal", obs: observation("baremetal", "htpc", 0, 1)}}
	started := time.Now()
	o, err := New(a, b).Observe(context.Background())
	if err != nil || len(o.Hosts) != 2 || o.Hosts[0].ID != "local" {
		t.Fatal(o, err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("runtimes observed sequentially: %v", elapsed)
	}
}

type forgettingPreparer struct {
	fakePreparer
	forgotten *[]string
}

func (p forgettingPreparer) Forget(l domain.Lease) error {
	*p.forgotten = append(*p.forgotten, p.name+":"+l.ID)
	return nil
}

func TestForgetRoutesByLeaseProvider(t *testing.T) {
	var forgotten []string
	p := NewPreparer(forgettingPreparer{fakePreparer{"vm"}, &forgotten}, map[string]Preparer{"baremetal": forgettingPreparer{fakePreparer{"node"}, &forgotten}})
	f := p.(interface{ Forget(domain.Lease) error })
	_ = f.Forget(domain.Lease{ID: "a", Provider: "tart"})
	_ = f.Forget(domain.Lease{ID: "b", Provider: "baremetal"})
	if strings.Join(forgotten, ",") != "vm:a,node:b" {
		t.Fatal(forgotten)
	}
}

type recordingBuilder struct {
	name  string
	steps *[]string
}

func (b recordingBuilder) Step(_ context.Context, l domain.Lease, _ domain.ImageProfile, step string, _ func(string) error) error {
	*b.steps = append(*b.steps, b.name+":"+step)
	return nil
}

func TestImageStepsRouteByLeaseProvider(t *testing.T) {
	var steps []string
	b := NewImageBuilder(recordingBuilder{"tart", &steps}, map[string]ImageBuilder{"baremetal": recordingBuilder{"node", &steps}})
	_ = b.Step(context.Background(), domain.Lease{}, domain.ImageProfile{}, "resolve", nil)
	_ = b.Step(context.Background(), domain.Lease{Provider: "baremetal"}, domain.ImageProfile{}, "commit", nil)
	if strings.Join(steps, ",") != "tart:resolve,node:commit" {
		t.Fatal(steps)
	}
	none := NewImageBuilder(nil, map[string]ImageBuilder{"baremetal": recordingBuilder{"node", &steps}})
	if err := none.Step(context.Background(), domain.Lease{}, domain.ImageProfile{}, "resolve", nil); err == nil {
		t.Fatal("routed a primary step without a primary builder")
	}
}

type dialingRuntime struct {
	fakeRuntime
	dialed []string
}

func (d *dialingRuntime) DialService(_ context.Context, l domain.Lease, service string) (net.Conn, error) {
	d.dialed = append(d.dialed, l.ID+":"+service)
	a, b := net.Pipe()
	b.Close()
	return a, nil
}

type passwordPreparer struct{ fakePreparer }

func (p passwordPreparer) LeasePassword(l domain.Lease) (string, error) {
	return p.name + ":" + l.ID, nil
}

func TestServiceDialAndPasswordRouteByProvider(t *testing.T) {
	node := &dialingRuntime{fakeRuntime: fakeRuntime{name: "baremetal"}}
	f := New(&fakeRuntime{name: "tart"}, node)
	c, err := f.DialService(context.Background(), domain.Lease{ID: "a", Provider: "baremetal"}, "desktop")
	if err != nil || strings.Join(node.dialed, ",") != "a:desktop" {
		t.Fatal(node.dialed, err)
	}
	c.Close()
	if _, err := f.DialService(context.Background(), domain.Lease{ID: "b"}, "desktop"); !errors.Is(err, provider.ErrNoServiceDialer) {
		t.Fatal("primary without a dialer:", err)
	}
	p := NewPreparer(passwordPreparer{fakePreparer{"vm"}}, map[string]Preparer{"baremetal": passwordPreparer{fakePreparer{"node"}}})
	pw := p.(interface {
		LeasePassword(domain.Lease) (string, error)
	})
	if got, _ := pw.LeasePassword(domain.Lease{ID: "c", Provider: "baremetal"}); got != "node:c" {
		t.Fatal(got)
	}
	if got, _ := pw.LeasePassword(domain.Lease{ID: "d"}); got != "vm:d" {
		t.Fatal(got)
	}
}

func TestObserveRejectsDuplicateHostOwnership(t *testing.T) {
	primary := &fakeRuntime{name: "tart", obs: observation("tart", "same-host", 0, 2)}
	secondary := &fakeRuntime{name: "baremetal", obs: observation("baremetal", "same-host", 0, 1)}
	if _, err := New(primary, secondary).Observe(context.Background()); err == nil {
		t.Fatal("ambiguous host ownership accepted")
	}
}
