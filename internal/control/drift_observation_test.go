package control

import (
	"context"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type driftObservationBackend struct {
	*fakeBackend
	change func(*domain.Observation)
}

func (b *driftObservationBackend) Observe(ctx context.Context) (domain.Observation, error) {
	o, err := b.fakeBackend.Observe(ctx)
	if err == nil {
		b.change(&o)
	}
	return o, err
}

func TestUnverifiedHostOrAddressCannotPermanentlyDriftReadyLease(t *testing.T) {
	for _, unverified := range []string{"offline-host", "offline-address-present", "offline-missing-vm", "unknown-address"} {
		t.Run(unverified, func(t *testing.T) {
			c, preparer, op := sshControllerOn(t, 22101)
			close(preparer.finish)
			c.wg.Wait()
			tick(t, c)
			original, err := c.Lease(context.Background(), op.Lease.ID)
			if err != nil || original.State != "ready" {
				t.Fatal(original, err)
			}
			mode := unverified
			c.backend = &driftObservationBackend{fakeBackend: c.backend.(*fakeBackend), change: func(o *domain.Observation) {
				host := domain.Host{ID: "node", Availability: "available", LastSeenAt: time.Now().UTC()}
				if mode == "offline-host" || mode == "offline-address-present" || mode == "offline-missing-vm" {
					host.Availability = "unavailable"
				}
				o.Hosts = []domain.Host{host}
				o.StorageContainers = []domain.StorageContainer{{ID: original.Location, HostID: host.ID}}
				for i, vm := range o.VMs {
					if vm.Key() != original.Key() {
						continue
					}
					if mode == "offline-missing-vm" {
						o.VMs = append(o.VMs[:i], o.VMs[i+1:]...)
						break
					}
					o.VMs[i].HostID = host.ID
					if mode == "offline-host" || mode == "unknown-address" {
						o.VMs[i].IP = ""
					} else if mode == "real-address-change" {
						o.VMs[i].IP = "192.168.64.99"
					}
				}
			}}
			for range 3 {
				tick(t, c)
			}
			current, err := c.Lease(context.Background(), original.ID)
			if err != nil || current.State != "ready" || current.IP != original.IP || current.SSHPort != original.SSHPort || current.SSH == nil || current.SSH.HostKey != original.SSH.HostKey {
				t.Fatal("unverified observation destroyed a verified identity", current.State, current.Error, err)
			}
			_, tunnelErr := c.OpenTunnel(context.Background(), original.ID, "ssh")
			code(t, tunnelErr, "backend_unavailable")
			mode = "recovered"
			tick(t, c)
			current, _ = c.Lease(context.Background(), original.ID)
			if current.State != "ready" || current.IP != original.IP {
				t.Fatal("recovery required destructive lease replacement", current.State)
			}
			mode = "real-address-change"
			tick(t, c)
			tick(t, c)
			current, _ = c.Lease(context.Background(), original.ID)
			if current.State != "needs_attention" || current.Error == nil || current.Error.Code != "vm_drift" {
				t.Fatal("verified endpoint change escaped drift detection", current.State, current.Error)
			}
		})
	}
}
