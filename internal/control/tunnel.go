package control

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// Tunnels forward encrypted SSH bytes only. They never terminate SSH, expose a
// guest service without authentication, or accept caller-selected destinations.
type tunnel struct {
	closed      bool
	listener    net.Listener
	mu          sync.Mutex
	connections map[net.Conn]bool
	clients     int
	lease       domain.Lease
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
func (c *Controller) OpenTunnel(ctx context.Context, id string) (domain.Tunnel, error) {
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
	if !exists || vm.State != "running" || vm.IP != l.IP {
		return domain.Tunnel{}, domain.Err("lease_unavailable", "SSH tunnel requires a running VM at the lease address")
	}
	if c.tunnels == nil {
		c.tunnels = map[string]*tunnel{}
	}
	if t := c.tunnels[id]; t != nil {
		return domain.Tunnel{Address: t.listener.Addr().String(), LeaseID: id}, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return domain.Tunnel{}, domain.Err("tunnel_unavailable", "Cannot allocate loopback SSH tunnel")
	}
	t := &tunnel{listener: ln, connections: map[net.Conn]bool{}, lease: l}
	c.tunnels[id] = t
	go t.serve()
	return domain.Tunnel{Address: ln.Addr().String(), LeaseID: id}, nil
}
func (c *Controller) CloseTunnel(ctx context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.store.Lease(ctx, id); err != nil {
		return err
	}
	if t := c.tunnels[id]; t != nil {
		t.close()
		delete(c.tunnels, id)
	}
	return nil
}
func (c *Controller) reconcileTunnels(ls []domain.Lease) {
	for id, t := range c.tunnels {
		keep := false
		for _, l := range ls {
			vm, exists := c.vm(l)
			if l.ID == id && l.State == "ready" && l.IP == t.lease.IP && l.ExpiresAt.After(c.now()) && exists && vm.State == "running" && vm.IP == l.IP {
				keep = true
				break
			}
		}
		if !keep {
			t.close()
			delete(c.tunnels, id)
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
			out, err := net.DialTimeout("tcp", net.JoinHostPort(t.lease.IP, strconv.Itoa(t.lease.SSH.Port)), 5*time.Second)
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
