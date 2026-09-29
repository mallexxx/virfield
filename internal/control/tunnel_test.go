package control

import (
	"context"
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
