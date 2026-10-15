package control

import (
	"context"
	"errors"
	"io"
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

func TestUnverifiedAddressPreservesExistingStreamAndPausesNewConnections(t *testing.T) {
	c, l := readyVMLease(t, "uncertain-stream")
	defer c.closeTunnels()
	c.dialDesktop = func(context.Context, domain.Lease) (net.Conn, error) {
		client, peer := net.Pipe()
		go func() { defer peer.Close(); _, _ = io.Copy(peer, peer) }()
		return client, nil
	}
	tunnel, err := c.OpenTunnel(context.Background(), l.ID, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	dial := func() net.Conn {
		conn, err := net.DialTimeout("tcp", tunnel.Address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	echo := func(conn net.Conn) {
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := conn.Write([]byte("verified-stream")); err != nil {
			t.Fatal("verified stream was interrupted", err)
		}
		data := make([]byte, len("verified-stream"))
		if _, err := io.ReadFull(conn, data); err != nil || string(data) != "verified-stream" {
			t.Fatal("verified stream was interrupted", err)
		}
	}
	established := dial()
	echo(established)
	b := c.backend.(*fakeBackend)
	b.mu.Lock()
	v := b.vms[l.Key()]
	v.IP = ""
	b.vms[l.Key()] = v
	b.mu.Unlock()
	tick(t, c)
	echo(established)
	_, err = c.OpenTunnel(context.Background(), l.ID, "desktop")
	code(t, err, "backend_unavailable")
	paused := dial()
	paused.SetDeadline(time.Now().Add(time.Second))
	_, _ = paused.Write([]byte("must-not-forward"))
	if _, err := paused.Read(make([]byte, 1)); err == nil {
		t.Fatal("paused listener forwarded a new unverified connection")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("paused listener did not reject the connection")
	}
	b.mu.Lock()
	v.IP = l.IP
	b.vms[l.Key()] = v
	b.mu.Unlock()
	tick(t, c)
	recovered, err := c.OpenTunnel(context.Background(), l.ID, "desktop")
	if err != nil || recovered.Address != tunnel.Address {
		t.Fatal("inventory recovery replaced a verified endpoint", recovered, err)
	}
	echo(dial())
	// A failed whole-fleet refresh must pause cached listeners immediately,
	// not only after the next successful inventory or the next tick.
	b.mu.Lock()
	b.err = errors.New("inventory unavailable")
	b.mu.Unlock()
	tick(t, c)
	echo(established)
	_, err = c.OpenTunnel(context.Background(), l.ID, "desktop")
	code(t, err, "backend_unavailable")
	paused = dial()
	paused.SetDeadline(time.Now().Add(time.Second))
	if _, err := paused.Read(make([]byte, 1)); err == nil {
		t.Fatal("failed inventory allowed a cached listener to forward")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("failed inventory did not pause the listener immediately")
	}
	b.mu.Lock()
	b.err = nil
	b.mu.Unlock()
	tick(t, c)
	echo(dial())
	// Cached clients must also observe the freshness deadline while a slow
	// refresh is still in flight and has not reported an error or ticked.
	c.mu.Lock()
	c.lastObservedMono = time.Now().Add(-11 * time.Second)
	c.mu.Unlock()
	echo(established)
	paused = dial()
	paused.SetDeadline(time.Now().Add(time.Second))
	if _, err := paused.Read(make([]byte, 1)); err == nil {
		t.Fatal("stale cached listener forwarded a new connection")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("stale cached listener failed to reject the connection")
	}
	tick(t, c)
	echo(dial())
	c.now = func() time.Time { return l.ExpiresAt.Add(time.Second) }
	b.mu.Lock()
	b.err = errors.New("inventory unavailable")
	b.mu.Unlock()
	tick(t, c)
	if conn, err := net.DialTimeout("tcp", tunnel.Address, time.Second); err == nil {
		conn.Close()
		t.Fatal("unverified inventory suppressed expiry revocation")
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
