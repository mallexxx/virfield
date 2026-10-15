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
	service        string
	closeReason    string
	lifecycle      *sync.WaitGroup
	record         func(domain.Diagnostic)
	closed         bool
	paused         bool
	listener       net.Listener
	mu             sync.Mutex
	connections    map[net.Conn]bool
	clients        int
	lease          domain.Lease
	dial           func(context.Context) (net.Conn, error)
	verifyEndpoint func() bool
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

func (t *tunnel) close(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	t.closeReason = reason
	t.listener.Close()
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
	if c.tunnelsClosed {
		return domain.Tunnel{}, domain.Err("service_stopping", "Tunnels are closed for daemon shutdown")
	}
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
	if !c.leaseObservationVerified(l, vm, exists) {
		return domain.Tunnel{}, &domain.Error{Code: "backend_unavailable", Message: "Lease host or address is unverified; wait for inventory recovery", Retryable: true}
	}
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
	t := &tunnel{listener: ln, connections: map[net.Conn]bool{}, lease: l, service: service, lifecycle: &c.tunnelWG}
	t.record = func(d domain.Diagnostic) {
		expected := expectedIdentity(t.lease)
		d.Expected = &expected
		d.Service = service
		c.recordDiagnostic(l.ID, d)
	}
	t.verifyEndpoint = func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		vm, exists := c.vm(l)
		return c.tunnels[key] == t && c.leaseObservationVerified(l, vm, exists) && exists && vm.State == "running" && vm.IP == l.IP && vm.SSHPort == l.SSHPort
	}
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
	c.listenerDiagnosticLocked(t, "opened", "ready")
	c.tunnelWG.Add(1)
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
		t.close("explicit_close")
		c.listenerDiagnosticLocked(t, "closed", "explicit_close")
		delete(c.tunnels, key)
	}
	return nil
}
func (c *Controller) reconcileTunnels(ls []domain.Lease) {
	for key, t := range c.tunnels {
		keep := false
		paused := false
		reason := "lease_unavailable"
		for _, l := range ls {
			if l.ID != t.lease.ID {
				continue
			}
			if !l.ExpiresAt.After(c.now()) {
				reason = "expiry"
				break
			}
			if l.State == "released" || l.State == "releasing" {
				reason = "release"
				break
			}
			if l.Error != nil && l.Error.Code == "vm_drift" {
				reason = "verified_drift"
				break
			}
			if l.State != "ready" || l.IP != t.lease.IP || l.SSHPort != t.lease.SSHPort {
				reason = "endpoint_changed"
				continue
			}
			vm, exists := c.vm(l)
			if !c.leaseObservationVerified(l, vm, exists) {
				// Keep established streams to their already verified peer. A
				// cached listener must reject new clients until fresh inventory
				// verifies the endpoint again, then resume on the same port.
				keep, paused = true, true
				break
			}
			if exists && vm.State == "running" && vm.IP == l.IP && vm.SSHPort == l.SSHPort {
				keep = true
				break
			}
			reason = "verified_endpoint_change"
		}
		if !keep {
			t.close(reason)
			c.listenerDiagnosticLocked(t, "closed", reason)
			delete(c.tunnels, key)
		} else {
			t.mu.Lock()
			changed := t.paused != paused
			t.paused = paused
			t.mu.Unlock()
			if changed {
				action, reason := "resumed", "inventory_verified"
				if paused {
					action, reason = "paused", "inventory_unverified"
				}
				c.listenerDiagnosticLocked(t, action, reason)
			}
		}
	}
}
func (c *Controller) closeTunnels() {
	c.mu.Lock()
	c.tunnelsClosed = true
	for id, t := range c.tunnels {
		t.close("daemon_shutdown")
		c.listenerDiagnosticLocked(t, "closed", "daemon_shutdown")
		delete(c.tunnels, id)
	}
	c.mu.Unlock()
	done := make(chan struct{})
	go func() { c.tunnelWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		c.log.Error("tunnel diagnostics did not drain before shutdown deadline")
	}
}

func (c *Controller) pauseTunnels() {
	for _, t := range c.tunnels {
		t.mu.Lock()
		changed := !t.paused
		t.paused = true
		t.mu.Unlock()
		if changed {
			c.listenerDiagnosticLocked(t, "paused", "backend_unavailable")
		}
	}
}

func (t *tunnel) serve() {
	defer t.lifecycle.Done()
	for {
		in, err := t.listener.Accept()
		if err != nil {
			return
		}
		t.mu.Lock()
		if t.closed || t.paused || t.clients >= 32 {
			reason := "connection_limit"
			if t.closed {
				reason = t.closeReason
			} else if t.paused {
				reason = "listener_paused"
			}
			t.mu.Unlock()
			in.Close()
			t.record(domain.Diagnostic{Kind: "tunnel.stream", Action: "rejected", Reason: reason, Summary: "Tunnel connection rejected: " + reason})
			continue
		}
		t.connections[in] = true
		t.clients++
		t.mu.Unlock()
		t.lifecycle.Add(1)
		go func() {
			defer t.lifecycle.Done()
			d := domain.Diagnostic{Kind: "tunnel.stream", Action: "terminated", Reason: "peer_eof"}
			defer func() {
				t.mu.Lock()
				forced := t.closeReason
				t.mu.Unlock()
				if forced != "" {
					d.Reason = forced
					d.BackendError = nil
				}
				d.Summary = "Tunnel stream terminated: " + d.Reason
				t.record(d)
			}()
			defer func() { in.Close(); t.mu.Lock(); delete(t.connections, in); t.clients--; t.mu.Unlock() }()
			if t.verifyEndpoint != nil && !t.verifyEndpoint() {
				d.Reason = "endpoint_unverified"
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			out, err := t.dial(ctx)
			cancel()
			if err != nil {
				d.Reason = "upstream_dial_failure"
				d.BackendError = diagnosticError(err)
				return
			}
			defer out.Close()
			if t.verifyEndpoint != nil && !t.verifyEndpoint() {
				d.Reason = "endpoint_changed"
				return
			}
			t.mu.Lock()
			if t.closed || t.paused {
				d.Reason = "listener_paused"
				t.mu.Unlock()
				return
			}
			t.connections[out] = true
			t.mu.Unlock()
			defer func() { t.mu.Lock(); delete(t.connections, out); t.mu.Unlock() }()
			done := make(chan copyResult, 1)
			failures := make(chan copyResult, 1)
			cleanup := make(chan struct{})
			recordFailure := func(result copyResult) {
				if result.err != nil {
					select {
					case failures <- result:
					default:
					}
				}
			}
			go func() {
				result := copyEndpoints(out, in, "guest", "client")
				select {
				case <-cleanup:
				default:
					recordFailure(result)
				}
				if result.err != nil {
					out.Close()
				}
				if tcp, ok := out.(*net.TCPConn); ok {
					tcp.CloseWrite()
				}
				done <- result
			}()
			result := copyEndpoints(in, out, "client", "guest")
			recordFailure(result)
			close(cleanup)
			in.Close()
			out.Close()
			up := <-done
			d.BytesToGuest, d.BytesToClient = up.bytes, result.bytes
			select {
			case failure := <-failures:
				d.Reason = "client_io"
				if failure.side == "guest" {
					d.Reason = "upstream_io"
				}
				d.Direction = failure.direction
				d.BackendError = diagnosticError(failure.err)
			default:
			}
		}()
	}
}

func (c *Controller) listenerDiagnosticLocked(t *tunnel, action, reason string) {
	expected := expectedIdentity(t.lease)
	c.recordDiagnosticLocked(t.lease.ID, domain.Diagnostic{Kind: "tunnel.listener", Action: action, Reason: reason, Summary: "Tunnel listener " + action + ": " + reason, Service: t.service, Expected: &expected})
}

type copyResult struct {
	bytes           int64
	err             error
	side, direction string
}
type observedReader struct {
	source io.Reader
	err    error
}

func (r *observedReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

type observedWriter struct {
	destination io.Writer
	err         error
}

func (w *observedWriter) Write(p []byte) (int, error) {
	n, err := w.destination.Write(p)
	if err != nil {
		w.err = err
	} else if n < len(p) {
		w.err = io.ErrShortWrite
	}
	return n, err
}
func copyEndpoints(dst, src net.Conn, dstSide, srcSide string) copyResult {
	r := &observedReader{source: src}
	w := &observedWriter{destination: dst}
	n, err := io.Copy(w, r)
	side := srcSide
	if w.err != nil {
		side = dstSide
	}
	return copyResult{bytes: n, err: err, side: side, direction: srcSide + "_to_" + dstSide}
}
