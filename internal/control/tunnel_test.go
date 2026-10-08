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
	_, err := c.OpenTunnel(ctx, op.Lease.ID, "ssh")
	code(t, err, "lease_unavailable")
	ready(t, c, op)
	l, _ := c.Lease(ctx, op.Lease.ID)
	l.SSH = &domain.SSHConnection{User: "lume", Port: 22}
	if err := s.Save(ctx, l, nil, "", "", "test.identity", "test identity"); err != nil {
		t.Fatal(err)
	}
	a, err := c.OpenTunnel(ctx, l.ID, "ssh")
	if err != nil {
		t.Fatal(err)
	}
	defer c.closeTunnels()
	b, err := c.OpenTunnel(ctx, l.ID, "ssh")
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
	_, err = c.OpenTunnel(ctx, l.ID, "ssh")
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
			tunnel, err := c.OpenTunnel(ctx, l.ID, "ssh")
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

type desktopPreparer struct{}

func (desktopPreparer) Prepare(context.Context, domain.Lease) (domain.SSHConnection, error) {
	return domain.SSHConnection{}, nil
}

func (desktopPreparer) LeasePassword(l domain.Lease) (string, error) { return "p@ss/" + l.ID, nil }

// readyVMLease makes a ready VM lease with a verified image and SSH identity.
func readyVMLease(t *testing.T, key string) (*Controller, domain.Lease) {
	t.Helper()
	c, s, _ := setup(t)
	ctx := context.Background()
	op := acquire(t, c, key)
	ready(t, c, op)
	l, _ := c.Lease(ctx, op.Lease.ID)
	l.SSH = &domain.SSHConnection{User: "admin", Port: 22}
	l.ImageID, l.GuestUser = "image-0123", "admin"
	if err := s.Save(ctx, l, nil, "", "", "test.identity", "test identity"); err != nil {
		t.Fatal(err)
	}
	c.leasePreparer = desktopPreparer{}
	return c, l
}

func TestDesktopTunnelReturnsVNCURLAndForwards(t *testing.T) {
	c, l := readyVMLease(t, "desktop-acquire")
	ctx := context.Background()
	screen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer screen.Close()
	go func() {
		if conn, err := screen.Accept(); err == nil {
			conn.Write([]byte("RFB 003.889\n"))
			conn.Close()
		}
	}()
	var dialed string
	c.dialDesktop = func(ctx context.Context, lease domain.Lease) (net.Conn, error) {
		dialed = lease.ID
		return net.Dial("tcp", screen.Addr().String())
	}
	defer c.closeTunnels()
	d, err := c.OpenTunnel(ctx, l.ID, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(d.Address)
	if d.Service != "desktop" || d.URL != "vnc://admin:p%40ss%2F"+l.ID+"@127.0.0.1:"+port {
		t.Fatal(d.Service, d.URL)
	}
	conn, err := net.Dial("tcp", d.Address)
	if err != nil {
		t.Fatal(err)
	}
	banner := make([]byte, 12)
	if _, err := conn.Read(banner); err != nil || string(banner) != "RFB 003.889\n" || dialed != l.ID {
		t.Fatal(string(banner), dialed, err)
	}
	conn.Close()
	s, err := c.OpenTunnel(ctx, l.ID, "ssh")
	if err != nil || s.Address == d.Address || s.URL != "" {
		t.Fatal("ssh and desktop tunnels are not independent", s, err)
	}
	if err := c.CloseTunnel(ctx, l.ID, "desktop"); err != nil {
		t.Fatal(err)
	}
	if c2, err := net.DialTimeout("tcp", s.Address, 100*time.Millisecond); err != nil {
		t.Fatal("closing desktop closed ssh", err)
	} else {
		c2.Close()
	}
	if _, err := c.Release(ctx, l.ID, "desktop-release"); err != nil {
		t.Fatal(err)
	}
	if c3, err := net.DialTimeout("tcp", s.Address, 100*time.Millisecond); err == nil {
		c3.Close()
		t.Fatal("released lease kept its ssh tunnel")
	}
}

func TestDesktopNeedsAVMLeaseAndAKnownService(t *testing.T) {
	c, l := readyVMLease(t, "desktop-host-acquire")
	ctx := context.Background()
	_, err := c.OpenTunnel(ctx, l.ID, "vnc")
	code(t, err, "invalid_request")
	l.ImageID = ""
	if err := c.store.Save(ctx, l, nil, "", "", "test.identity", "host lease"); err != nil {
		t.Fatal(err)
	}
	_, err = c.OpenTunnel(ctx, l.ID, "desktop")
	code(t, err, "lease_unavailable")
}
