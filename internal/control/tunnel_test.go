package control

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestTunnelBoundToReadyLeaseAndRevokedOnRelease(t *testing.T) {
	c, s, _ := setup(t)
	ctx := context.Background()
	op := acquire(t, c, "tunnel-acquire")
	_, err := c.OpenTunnel(ctx, op.Lease.ID)
	code(t, err, "lease_unavailable")
	ready(t, c, op)
	l, _ := c.Lease(ctx, op.Lease.ID)
	l.SSH = &domain.SSHConnection{User: "lume", Port: 22}
	if err := s.Save(ctx, l, nil, "", "", "test.identity", "test identity"); err != nil {
		t.Fatal(err)
	}
	a, err := c.OpenTunnel(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer c.closeTunnels()
	b, err := c.OpenTunnel(ctx, l.ID)
	if err != nil || a != b {
		t.Fatal("duplicate tunnel", err)
	}
	if _, err := c.Release(ctx, l.ID, "tunnel-release"); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", a.Address, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("released tunnel still listening")
	}
	_, err = c.OpenTunnel(ctx, l.ID)
	code(t, err, "lease_unavailable")
}

func TestTunnelRevokedOnExpiryWithUnavailableBackendAndOnDrift(t *testing.T) {
	for _, failure := range []string{"expired-unavailable", "stopped"} {
		t.Run(failure, func(t *testing.T) {
			c, s, backend := setup(t)
			ctx := context.Background()
			op := acquire(t, c, "tunnel-failure-acquire")
			ready(t, c, op)
			l, _ := c.Lease(ctx, op.Lease.ID)
			l.SSH = &domain.SSHConnection{User: "lume", Port: 22}
			if err := s.Save(ctx, l, nil, "", "", "test.identity", "test"); err != nil {
				t.Fatal(err)
			}
			tunnel, err := c.OpenTunnel(ctx, l.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer c.closeTunnels()
			if failure == "expired-unavailable" {
				c.now = func() time.Time { return l.ExpiresAt.Add(time.Second) }
				backend.err = errors.New("Lume unavailable")
			} else {
				vm := backend.vms[l.Key()]
				vm.State = "stopped"
				backend.vms[l.Key()] = vm
			}
			tick(t, c)
			conn, err := net.DialTimeout("tcp", tunnel.Address, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				t.Fatal("tunnel remained open after expiry or observed VM drift")
			}
			if backend.count("delete") != 0 || backend.count("stop") != 0 {
				t.Fatal("tunnel revocation must not force VM deletion")
			}
		})
	}
}
