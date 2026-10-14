// Package fleet composes the primary runtime (this Mac's VMs) with secondary
// runtimes such as SSH-controlled physical nodes. Lifecycle calls route by the
// template's or lease's provider; an empty provider means the primary.
package fleet

import (
	"context"
	"errors"
	"net"
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
			if _, exists := owners[h.ID]; exists {
				return domain.Observation{}, domain.Err("backend_unavailable", "Host identity overlaps across runtime inventories: "+h.ID)
			}
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
// DialService routes a lease service connection to the lease's runtime.
func (f *Fleet) DialService(ctx context.Context, l domain.Lease, service string) (net.Conn, error) {
	r, err := f.runtime(l.Provider)
	if err != nil {
		return nil, err
	}
	d, ok := r.(provider.ServiceDialer)
	if !ok {
		return nil, provider.ErrNoServiceDialer
	}
	return d.DialService(ctx, l, service)
}

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

// Forget and ForgetImage remove private lease and image credentials; the
// controller calls them after confirmed deletion. A lease's credentials
// belong to its runtime; image credentials live with the primary.
func (p *routedPreparer) Forget(l domain.Lease) error {
	owner := p.primary
	if routed, ok := p.byProvider[l.Provider]; ok {
		owner = routed
	}
	if f, ok := owner.(interface{ Forget(domain.Lease) error }); ok {
		return f.Forget(l)
	}
	return nil
}

// LeasePassword returns a lease's guest password from its runtime.
func (p *routedPreparer) LeasePassword(l domain.Lease) (string, error) {
	owner := p.primary
	if routed, ok := p.byProvider[l.Provider]; ok {
		owner = routed
	}
	if f, ok := owner.(interface {
		LeasePassword(domain.Lease) (string, error)
	}); ok {
		return f.LeasePassword(l)
	}
	return "", domain.Err("lease_unavailable", "Lease has no guest password")
}

func (p *routedPreparer) ForgetImage(l domain.Lease) error {
	if f, ok := p.primary.(interface{ ForgetImage(domain.Lease) error }); ok {
		return f.ForgetImage(l)
	}
	return nil
}

// ImageBuilder runs one image build phase; each runtime builds its own images.
type ImageBuilder interface {
	Step(ctx context.Context, l domain.Lease, p domain.ImageProfile, step string, progress func(string) error) error
}

type routedImageBuilder struct {
	primary    ImageBuilder
	byProvider map[string]ImageBuilder
}

// NewImageBuilder routes image steps by the image lease's provider; primary
// may be nil when only secondary runtimes build images.
func NewImageBuilder(primary ImageBuilder, byProvider map[string]ImageBuilder) ImageBuilder {
	return &routedImageBuilder{primary: primary, byProvider: byProvider}
}

func (b *routedImageBuilder) Step(ctx context.Context, l domain.Lease, p domain.ImageProfile, step string, progress func(string) error) error {
	if routed, ok := b.byProvider[l.Provider]; ok {
		return routed.Step(ctx, l, p, step, progress)
	}
	if b.primary == nil {
		return domain.Err("invalid_profile", "No image builder for this runtime")
	}
	return b.primary.Step(ctx, l, p, step, progress)
}

// ExportStep preserves the primary builder's portable-export capability when
// a node builder is composed beside it. Node publication remains unsupported.
func (b *routedImageBuilder) ExportStep(ctx context.Context, l domain.Lease, target domain.RegistryExport, step string) (string, error) {
	if l.Provider != "" || l.Source != nil && l.Source.Image != nil && l.Source.Image.Node != nil {
		return "", domain.Err("registry_push_disabled", "Node images do not support portable Tart publication")
	}
	exporter, ok := b.primary.(interface {
		ExportStep(context.Context, domain.Lease, domain.RegistryExport, string) (string, error)
	})
	if !ok {
		return "", domain.Err("registry_push_disabled", "Primary image builder does not support publication")
	}
	return exporter.ExportStep(ctx, l, target, step)
}

func (b *routedImageBuilder) CachedXcode(x domain.XcodeRelease) bool {
	if cache, ok := b.primary.(interface {
		CachedXcode(domain.XcodeRelease) bool
	}); ok {
		return cache.CachedXcode(x)
	}
	return false
}

func (b *routedImageBuilder) ValidateTemplate(t domain.Template) error {
	builder := b.primary
	if routed, ok := b.byProvider[t.Provider]; ok {
		builder = routed
	}
	if builder == nil {
		return domain.Err("image_dependency", "No builder is configured for the image runtime")
	}
	if v, ok := builder.(interface{ ValidateTemplate(domain.Template) error }); ok {
		return v.ValidateTemplate(t)
	}
	return nil
}
