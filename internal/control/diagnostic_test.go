package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mallexxx/virfield/internal/domain"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitDiagnostic(t *testing.T, c *Controller, id, kind, reason string) domain.Diagnostic {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		events, err := c.store.Events(context.Background(), 0, id, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if d := event.Diagnostic; d != nil && d.Kind == kind && d.Reason == reason {
				return *d
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("No %s/%s diagnostic for %s", kind, reason, id)
	return domain.Diagnostic{}
}

func TestDriftRetainsBothEndpointsBeforeClearingLeaseAddress(t *testing.T) {
	c, l := readyVMLease(t, "diagnostic-drift")
	b := c.backend.(*fakeBackend)
	old := l.IP
	v := b.vms[l.Key()]
	v.IP = "192.168.64.99"
	v.SSHPort = 2222
	b.vms[l.Key()] = v
	tick(t, c)
	tick(t, c)
	changed, err := c.Lease(context.Background(), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed.IP != "" || changed.SSHPort != 0 || changed.State != "needs_attention" {
		t.Fatal(changed)
	}
	d := waitDiagnostic(t, c, l.ID, "lease.drift", "verified_drift")
	if d.Expected.IP != old || d.Observed.IP != v.IP || d.Observed.SSHPort != 2222 || d.Expected.VMName != l.VMName || d.ObservationAt.IsZero() {
		t.Fatal(d)
	}
}

func TestTunnelTerminationDiagnosticsCoverEveryForcedCause(t *testing.T) {
	for _, reason := range []string{"explicit_close", "release", "expiry", "verified_endpoint_change", "daemon_shutdown"} {
		t.Run(reason, func(t *testing.T) {
			c, l := readyVMLease(t, "diagnostic-"+reason)
			t.Cleanup(c.closeTunnels)
			c.dialDesktop = func(context.Context, domain.Lease) (net.Conn, error) {
				client, peer := net.Pipe()
				go func() { defer peer.Close(); _, _ = io.Copy(peer, peer) }()
				return client, nil
			}
			tunnel, err := c.OpenTunnel(context.Background(), l.ID, "desktop")
			if err != nil {
				t.Fatal(err)
			}
			conn, err := net.DialTimeout("tcp", tunnel.Address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			payload := "private-stream-payload-do-not-record"
			if _, err := conn.Write([]byte(payload)); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, len(payload))
			if _, err := io.ReadFull(conn, buf); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "explicit_close":
				err = c.CloseTunnel(context.Background(), l.ID, "desktop")
			case "release":
				_, err = c.Release(context.Background(), l.ID, "diagnostic-release-close")
			case "expiry":
				c.now = func() time.Time { return l.ExpiresAt.Add(time.Second) }
				tick(t, c)
			case "verified_endpoint_change":
				b := c.backend.(*fakeBackend)
				v := b.vms[l.Key()]
				v.State = "stopped"
				b.vms[l.Key()] = v
				tick(t, c)
			case "daemon_shutdown":
				c.closeTunnels()
			}
			if err != nil {
				t.Fatal(err)
			}
			d := waitDiagnostic(t, c, l.ID, "tunnel.stream", reason)
			if d.Service != "desktop" || d.Expected.IP != l.IP || d.BytesToGuest < int64(len(payload)) {
				t.Fatal(d)
			}
			events, _ := c.store.Events(context.Background(), 0, l.ID, 500)
			data, _ := json.Marshal(events)
			if strings.Contains(string(data), payload) || strings.Contains(string(data), "vnc://") || strings.Contains(string(data), "p@ss/") {
				t.Fatal("Diagnostic retained protocol payload or desktop credentials")
			}
		})
	}
}

func TestBackendPauseResumeAndSafeOriginalErrorFingerprint(t *testing.T) {
	c, l := readyVMLease(t, "diagnostic-pause-resume")
	defer c.closeTunnels()
	if _, err := c.OpenTunnel(context.Background(), l.ID, "desktop"); err != nil {
		t.Fatal(err)
	}
	b := c.backend.(*fakeBackend)
	b.err = errors.New("token=private-secret vnc://admin:password@guest raw-payload")
	tick(t, c)
	d := waitDiagnostic(t, c, l.ID, "backend.observation", "backend_unavailable")
	if d.BackendError == nil || len(d.BackendError.FingerprintSHA256) != 64 || d.BackendError.Code != "unclassified" {
		t.Fatal(d)
	}
	data, _ := json.Marshal(d)
	if strings.Contains(string(data), "private-secret") || strings.Contains(string(data), "vnc://") || strings.Contains(string(data), "raw-payload") {
		t.Fatal(string(data))
	}
	waitDiagnostic(t, c, l.ID, "tunnel.listener", "backend_unavailable")
	b.err = nil
	tick(t, c)
	recovered := waitDiagnostic(t, c, l.ID, "backend.observation", "inventory_verified")
	if !recovered.ObservationVerified {
		t.Fatal("Recovered inventory diagnostic remained unverified", recovered)
	}
	waitDiagnostic(t, c, l.ID, "tunnel.listener", "inventory_verified")
	if d := diagnosticError(&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}); d.Code != fmt.Sprintf("errno_%d", int(syscall.ECONNRESET)) || d.Message != syscall.ECONNRESET.Error() {
		t.Fatal(d)
	}
}

type resetOnEOF struct{ net.Conn }

func (c resetOnEOF) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err == io.EOF {
		err = syscall.ECONNRESET
	}
	return n, err
}

func TestStreamDistinguishesUpstreamFailureFromGracefulEOF(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint(reset), func(t *testing.T) {
			c, l := readyVMLease(t, "diagnostic-upstream-"+fmt.Sprint(reset))
			defer c.closeTunnels()
			c.dialDesktop = func(context.Context, domain.Lease) (net.Conn, error) {
				client, peer := net.Pipe()
				go func() { _, _ = peer.Write([]byte("private-upstream-marker")); peer.Close() }()
				if reset {
					return resetOnEOF{client}, nil
				}
				return client, nil
			}
			tunnel, err := c.OpenTunnel(context.Background(), l.ID, "desktop")
			if err != nil {
				t.Fatal(err)
			}
			conn, err := net.DialTimeout("tcp", tunnel.Address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(conn)
			if err != nil || string(data) != "private-upstream-marker" {
				t.Fatal(string(data), err)
			}
			reason := "peer_eof"
			if reset {
				reason = "upstream_io"
			}
			d := waitDiagnostic(t, c, l.ID, "tunnel.stream", reason)
			if reset && (d.BackendError == nil || d.BackendError.Message != syscall.ECONNRESET.Error() || d.Direction != "guest_to_client") {
				t.Fatal(d)
			}
			if !reset && d.BackendError != nil {
				t.Fatal("Our stream cleanup was misreported as I/O failure", d)
			}
		})
	}
}
