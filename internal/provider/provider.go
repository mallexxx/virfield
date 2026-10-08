// Package provider defines the narrow runtime boundary owned by the control
// plane. Providers manage infrastructure effects; they never execute user
// workloads or decide lease policy.
package provider

import (
	"context"
	"errors"
	"net"

	"github.com/mallexxx/virfield/internal/domain"
)

// Runtime is deliberately smaller than any upstream runtime API. Mutations are
// never retried by the controller after an ambiguous result.
type Runtime interface {
	Observe(context.Context) (domain.Observation, error)
	Clone(context.Context, domain.Template, domain.Lease) error
	Start(context.Context, domain.Lease) error
	Stop(context.Context, domain.Lease) error
	Delete(context.Context, domain.Lease) error
}

// Named lets status and errors describe the selected dependency without
// coupling the controller to a concrete package.
type Named interface {
	ProviderName() string
}

// CreateStartsVM is implemented by controllers such as Orchard whose create
// operation atomically expresses both clone and desired running state.
type CreateStartsVM interface {
	CreateStartsVM() bool
}

// ServiceDialer is optional: it reaches a lease's private service endpoint,
// such as its desktop, where the controller cannot dial the lease address.
type ServiceDialer interface {
	DialService(ctx context.Context, l domain.Lease, service string) (net.Conn, error)
}

var ErrNoServiceDialer = errors.New("runtime has no service dialer")

// PowerController is optional. A runtime exposes it only when it has a tested
// relay capable of waking that specific physical host.
type PowerController interface {
	WakeHost(context.Context, string) error
}

// InventorySeed exposes configured identities before the first successful
// observation. It prevents offline hosts and removable storage from vanishing
// after a controller restart.
type InventorySeed interface {
	ConfiguredInventory() domain.Observation
}

func Name(runtime Runtime) string {
	if named, ok := runtime.(Named); ok && named.ProviderName() != "" {
		return named.ProviderName()
	}
	return "runtime"
}

func StartsOnClone(runtime Runtime) bool {
	combined, ok := runtime.(CreateStartsVM)
	return ok && combined.CreateStartsVM()
}
