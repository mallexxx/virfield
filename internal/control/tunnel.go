package control

import (
	"context"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/provider"
)

// Tunnels forward bytes of one lease service: encrypted SSH, or the guest's
// Screen Sharing, which authenticates with the lease password. They never
// terminate a protocol or accept caller-selected destinations.
type tunnel struct {
	closed      bool
	listener    net.Listener
	mu          sync.Mutex
	connections map[net.Conn]bool
	clients     int
	lease       domain.Lease
	dial        func(context.Context) (net.Conn, error)
}

func tunnelKey(id, service string) string { return id + "/" + service }

// desktopDial reaches a lease's Screen Sharing: through its runtime when the
// runtime owns a private path (a node VM), else at the lease address.
func (c *Controller) desktopDial(ctx context.Context, l domain.Lease) (net.Conn, error) {
	if c.dialDesktop != nil {
		return c.dialDesktop(ctx, l)
	}
	if d, ok := c.backend.(provider.ServiceDialer); ok && c.secondaryProvider(l.Provider) {
		return d.DialService(ctx, l, "desktop")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	return dialer.DialContext(ctx, "tcp", net.JoinHostPort(l.IP, "5900"))
}

// desktopURL signs in to Screen Sharing as the lease account.
func (c *Controller) desktopURL(l domain.Lease, address string) (string, error) {
	p, ok := c.leasePreparer.(interface {
		LeasePassword(domain.Lease) (string, error)
	})
	if !ok {
		return "", domain.Err("lease_unavailable", "Desktop requires per-lease guest credentials")
	}
	password, err := p.LeasePassword(l)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "vnc", User: url.UserPassword(l.GuestUser, password), Host: address}
	return u.String(), nil
}

func (t *tunnel) close() {
	t.listener.Close()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	for c := range t.connections {
		c.Close()
	}
}
func (c *Controller) OpenTunnel(ctx context.Context, id, service string) (domain.Tunnel, error) {
	if service != "ssh" && service != "desktop" {
		return domain.Tunnel{}, domain.Err("invalid_request", "service must be ssh or desktop")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.store.Lease(ctx, id)
	if err != nil {
		return domain.Tunnel{}, err
	}
	if err := c.healthy(); err != nil {
		return domain.Tunnel{}, err
	}
	if l.State != "ready" || l.SSH == nil || l.SSH.Port < 1 || l.SSH.Port > 65535 || !l.ExpiresAt.After(c.now()) {
		return domain.Tunnel{}, domain.Err("lease_unavailable", "SSH tunnel requires a ready unexpired lease")
	}
	vm, exists := c.vm(l)
	if !exists || vm.State != "running" || vm.IP != l.IP || vm.SSHPort != l.SSHPort {
		return domain.Tunnel{}, domain.Err("lease_unavailable", "SSH tunnel requires a running VM at the lease address")
	}
	// Only VM leases have a guest desktop signed in with a lease password.
	if service == "desktop" && l.ImageID == "" {
		return domain.Tunnel{}, domain.Err("lease_unavailable", "Desktop requires a VM lease")
	}
	if c.tunnels == nil {
		c.tunnels = map[string]*tunnel{}
	}
	key := tunnelKey(id, service)
	if t := c.tunnels[key]; t != nil {
		return c.describeTunnel(t, service)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return domain.Tunnel{}, domain.Err("tunnel_unavailable", "Cannot allocate loopback tunnel")
	}
	t := &tunnel{listener: ln, connections: map[net.Conn]bool{}, lease: l}
	t.dial = func(ctx context.Context) (net.Conn, error) {
		dialer := net.Dialer{Timeout: 5 * time.Second}
		return dialer.DialContext(ctx, "tcp", net.JoinHostPort(l.IP, strconv.Itoa(l.SSH.Port)))
	}
	if service == "desktop" {
		t.dial = func(ctx context.Context) (net.Conn, error) { return c.desktopDial(ctx, l) }
	}
	tunnel, err := c.describeTunnel(t, service)
	if err != nil {
		ln.Close()
		return domain.Tunnel{}, err
	}
	c.tunnels[key] = t
	go t.serve()
	return tunnel, nil
}

func (c *Controller) describeTunnel(t *tunnel, service string) (domain.Tunnel, error) {
	address := t.listener.Addr().String()
	result := domain.Tunnel{Address: address, LeaseID: t.lease.ID, Service: service}
	if service == "desktop" {
		u, err := c.desktopURL(t.lease, address)
		if err != nil {
			return domain.Tunnel{}, err
		}
		result.URL = u
	}
	return result, nil
}

func (c *Controller) CloseTunnel(ctx context.Context, id, service string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.store.Lease(ctx, id); err != nil {
		return err
	}
	key := tunnelKey(id, service)
	if t := c.tunnels[key]; t != nil {
		t.close()
		delete(c.tunnels, key)
	}
	return nil
}
func (c *Controller) reconcileTunnels(ls []domain.Lease) {
	for key, t := range c.tunnels {
		keep := false
		for _, l := range ls {
			vm, exists := c.vm(l)
			if l.ID == t.lease.ID && l.State == "ready" && l.IP == t.lease.IP && l.ExpiresAt.After(c.now()) && exists && vm.State == "running" && vm.IP == l.IP && vm.SSHPort == l.SSHPort && l.SSHPort == t.lease.SSHPort {
				keep = true
				break
			}
		}
		if !keep {
			t.close()
			delete(c.tunnels, key)
		}
	}
}
func (c *Controller) closeTunnels() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, t := range c.tunnels {
		t.close()
		delete(c.tunnels, id)
	}
}
func (t *tunnel) serve() {
	for {
		in, err := t.listener.Accept()
		if err != nil {
			return
		}
		t.mu.Lock()
		if t.closed || t.clients >= 32 {
			t.mu.Unlock()
			in.Close()
			continue
		}
		t.connections[in] = true
		t.clients++
		t.mu.Unlock()
		go func() {
			defer func() { in.Close(); t.mu.Lock(); delete(t.connections, in); t.clients--; t.mu.Unlock() }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			out, err := t.dial(ctx)
			cancel()
			if err != nil {
				return
			}
			defer out.Close()
			t.mu.Lock()
			if t.closed {
				t.mu.Unlock()
				return
			}
			t.connections[out] = true
			t.mu.Unlock()
			defer func() { t.mu.Lock(); delete(t.connections, out); t.mu.Unlock() }()
			done := make(chan struct{})
			go func() {
				io.Copy(out, in)
				if tcp, ok := out.(*net.TCPConn); ok {
					tcp.CloseWrite()
				}
				close(done)
			}()
			io.Copy(in, out)
			in.Close()
			out.Close()
			<-done
		}()
	}
}
