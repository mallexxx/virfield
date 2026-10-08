// Package fleet composes the primary runtime (this Mac's VMs) with secondary
// runtimes such as SSH-controlled physical nodes. Lifecycle calls route by the
// template's or lease's provider; an empty provider means the primary.
package fleet

import (
	"context"
	"errors"
	"sync"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/provider"
)

type Fleet struct {
	primary     provider.Runtime
	secondaries map[string]provider.Runtime
	order       []provider.Runtime
	mu          sync.Mutex
	owners      map[string]provider.Runtime
}

func New(primary provider.Runtime, secondaries ...provider.Runtime) *Fleet {
	f := &Fleet{primary: primary, secondaries: map[string]provider.Runtime{}, order: []provider.Runtime{primary}, owners: map[string]provider.Runtime{}}
	for _, r := range secondaries {
		f.secondaries[provider.Name(r)] = r
		f.order = append(f.order, r)
	}
	return f
}

// ProviderName keeps the primary's name for status and error text.
func (f *Fleet) ProviderName() string { return provider.Name(f.primary) }

// CreateStartsVM follows the primary; secondaries clone and start separately.
func (f *Fleet) CreateStartsVM() bool { return provider.StartsOnClone(f.primary) }

func (f *Fleet) runtime(name string) (provider.Runtime, error) {
	if name == "" || name == provider.Name(f.primary) {
		return f.primary, nil
	}
	if r, ok := f.secondaries[name]; ok {
		return r, nil
	}
	return nil, domain.Err("invalid_profile", "No runtime named "+name)
}

// Observe merges every runtime's inventory. Any failure pauses admission, so
// a lease VM can never look absent because its runtime was unreadable.
func (f *Fleet) Observe(ctx context.Context) (domain.Observation, error) {
	// Observe concurrently: a refresh costs the slowest runtime, and the
	// controller treats inventory older than ten seconds as stale.
	results := make([]domain.Observation, len(f.order))
	errs := make([]error, len(f.order))
	var wg sync.WaitGroup
	for i, r := range f.order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = r.Observe(ctx)
		}()
	}
	wg.Wait()
	merged := domain.Observation{Provider: f.ProviderName(), VMs: []domain.VM{}}
	owners := map[string]provider.Runtime{}
	for i, r := range f.order {
		o, err := results[i], errs[i]
		if err != nil {
			return domain.Observation{}, err
		}
		merged.VMs = append(merged.VMs, o.VMs...)
		merged.Hosts = append(merged.Hosts, o.Hosts...)
		merged.StorageContainers = append(merged.StorageContainers, o.StorageContainers...)
		merged.HostUsed += o.HostUsed
		merged.HostMax += o.HostMax
		if merged.At.IsZero() || o.At.Before(merged.At) {
			merged.At = o.At
		}
		for _, h := range o.Hosts {
			owners[h.ID] = r
		}
	}
	f.mu.Lock()
	f.owners = owners
	f.mu.Unlock()
	return merged, nil
}

// ConfiguredInventory merges the configured identities of every runtime.
func (f *Fleet) ConfiguredInventory() domain.Observation {
	merged := domain.Observation{Provider: f.ProviderName(), VMs: []domain.VM{}}
	for _, r := range f.order {
		seed, ok := r.(provider.InventorySeed)
		if !ok {
			continue
		}
		o := seed.ConfiguredInventory()
		merged.VMs = append(merged.VMs, o.VMs...)
		merged.Hosts = append(merged.Hosts, o.Hosts...)
		merged.StorageContainers = append(merged.StorageContainers, o.StorageContainers...)
		merged.HostMax += o.HostMax
	}
	return merged
}

func (f *Fleet) Clone(ctx context.Context, t domain.Template, l domain.Lease) error {
	r, err := f.runtime(t.Provider)
	if err != nil {
		return err
	}
	return r.Clone(ctx, t, l)
}

func (f *Fleet) Start(ctx context.Context, l domain.Lease) error {
	r, err := f.runtime(l.Provider)
	if err != nil {
		return err
	}
	return r.Start(ctx, l)
}

func (f *Fleet) Stop(ctx context.Context, l domain.Lease) error {
	r, err := f.runtime(l.Provider)
	if err != nil {
		return err
	}
	return r.Stop(ctx, l)
}

func (f *Fleet) Delete(ctx context.Context, l domain.Lease) error {
	r, err := f.runtime(l.Provider)
	if err != nil {
		return err
	}
	return r.Delete(ctx, l)
}

// WakeHost asks the runtime that last reported the host.
func (f *Fleet) WakeHost(ctx context.Context, hostID string) error {
	f.mu.Lock()
	r := f.owners[hostID]
	f.mu.Unlock()
	power, ok := r.(provider.PowerController)
	if !ok {
		return errors.New("host has no runtime that can wake it")
	}
	return power.WakeHost(ctx, hostID)
}

// Preparer isolates lease SSH credentials; each runtime has its own method.
type Preparer interface {
	Prepare(context.Context, domain.Lease) (domain.SSHConnection, error)
}

type routedPreparer struct {
	primary    Preparer
	byProvider map[string]Preparer
}

func NewPreparer(primary Preparer, byProvider map[string]Preparer) Preparer {
	return &routedPreparer{primary: primary, byProvider: byProvider}
}

func (p *routedPreparer) Prepare(ctx context.Context, l domain.Lease) (domain.SSHConnection, error) {
	if routed, ok := p.byProvider[l.Provider]; ok {
		return routed.Prepare(ctx, l)
	}
	return p.primary.Prepare(ctx, l)
}

// Forget and ForgetImage remove the primary's private lease and image
// credentials; the controller calls them after confirmed deletion.
func (p *routedPreparer) Forget(l domain.Lease) error {
	if _, routed := p.byProvider[l.Provider]; routed {
		return nil
	}
	if f, ok := p.primary.(interface{ Forget(domain.Lease) error }); ok {
		return f.Forget(l)
	}
	return nil
}

func (p *routedPreparer) ForgetImage(l domain.Lease) error {
	if f, ok := p.primary.(interface{ ForgetImage(domain.Lease) error }); ok {
		return f.ForgetImage(l)
	}
	return nil
}
