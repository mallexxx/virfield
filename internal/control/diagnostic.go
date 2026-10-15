package control

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func runtimeIdentity(vm domain.VM, exists bool, at time.Time) domain.RuntimeIdentity {
	ip := ""
	if parsed := net.ParseIP(vm.IP); parsed != nil {
		ip = parsed.String()
	}
	return domain.RuntimeIdentity{VMName: vm.Name, Location: vm.Location, Provider: vm.Provider, HostID: vm.HostID, ContainerID: vm.ContainerID, OS: vm.OS, Resources: vm.Resources, State: vm.State, IP: ip, SSHPort: vm.SSHPort, Exists: exists, ObservedAt: at, LastSeenAt: vm.LastSeenAt}
}

func expectedIdentity(l domain.Lease) domain.RuntimeIdentity {
	if l.VerifiedRuntime != nil {
		return *l.VerifiedRuntime
	}
	return domain.RuntimeIdentity{VMName: l.VMName, Location: l.Location, Provider: l.Provider, Resources: l.Resources, State: "running", IP: l.IP, SSHPort: l.SSHPort, Exists: true}
}

// Unstructured backend errors can contain command arguments or remote output.
// Preserve their original fingerprint/type, never that text. Known errno and
// context causes retain their exact safe reason; typed API error codes survive.
func diagnosticError(err error) *domain.DiagnosticError {
	if err == nil {
		return nil
	}
	fingerprint := sha256.Sum256([]byte(err.Error()))
	d := &domain.DiagnosticError{Type: fmt.Sprintf("%T", err), Code: "unclassified", Message: "Unstructured backend error; match its fingerprint against private stage logs", FingerprintSHA256: fmt.Sprintf("%x", fingerprint)}
	var errno syscall.Errno
	var fault *domain.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		d.Code = "deadline_exceeded"
		d.Message = context.DeadlineExceeded.Error()
	case errors.Is(err, context.Canceled):
		d.Code = "canceled"
		d.Message = context.Canceled.Error()
	case errors.Is(err, net.ErrClosed):
		d.Code = "connection_closed"
		d.Message = net.ErrClosed.Error()
	case errors.As(err, &errno):
		d.Code = fmt.Sprintf("errno_%d", int(errno))
		d.Message = errno.Error()
	case errors.As(err, &fault):
		if domain.ValidName(fault.Code) {
			d.Code = fault.Code
			d.Message = "Typed backend failure: " + fault.Code
		}
	}
	return d
}

// Called while c.mu is held. Diagnostics never change lifecycle timestamps,
// lease state, deadlines or keys. Stream callbacks take the lock before reading
// the current lease, so a late close cannot overwrite release or renewal.
func (c *Controller) recordDiagnosticLocked(id string, d domain.Diagnostic) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := c.store.Lease(ctx, id)
	if err == nil {
		d.Version = 1
		d.At = c.now()
		if d.Expected == nil {
			expected := expectedIdentity(l)
			d.Expected = &expected
		}
		if d.Observed == nil {
			vm, exists := c.vm(l)
			observed := runtimeIdentity(vm, exists, c.observation.At)
			d.Observed = &observed
		}
		vm, exists := c.vm(l)
		d.ObservationVerified = c.healthy() == nil && c.leaseObservationVerified(l, vm, exists)
		hostID := vm.HostID
		if hostID == "" && c.secondaryProvider(l.Provider) {
			hostID = l.Location
		}
		for _, host := range c.observation.Hosts {
			if host.ID == hostID {
				d.HostAvailability = host.Availability
				if domain.ValidName(host.UnavailableReason) {
					d.HostUnavailableReason = host.UnavailableReason
				} else if host.UnavailableReason != "" {
					d.HostUnavailableReason = "unclassified"
				}
				break
			}
		}
		d.ObservationAt = c.observation.At
		err = c.store.SaveDiagnostic(ctx, l, d)
	}
	if err != nil {
		c.log.Error("persist lease diagnostic", "lease_id", id, "kind", d.Kind, "error", err)
	}
}

func (c *Controller) recordDiagnostic(id string, d domain.Diagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recordDiagnosticLocked(id, d)
}
