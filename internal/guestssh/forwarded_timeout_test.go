package guestssh

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// SSH forwarding channels do not implement net.Conn deadlines. Reproduce that
// transport contract without depending on a real node or weakening host pins.
type withoutDeadlines struct{ net.Conn }

func (withoutDeadlines) SetDeadline(time.Time) error { return errors.New("deadline not supported") }

func TestGuestCommandTimeoutWorksOnForwardedConnections(t *testing.T) {
	port, _ := sshServer(t, "VirtualMac2,1")
	manager, lease := testImageGuest(t, port)
	guest, err := manager.Connect(context.Background(), lease, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	guest.raw = withoutDeadlines{guest.raw}
	done := make(chan error, 1)
	go func() {
		_, err := guest.RunReader(context.Background(), 50*time.Millisecond, "block", strings.NewReader(""))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("Forwarded command lost its timeout", err)
		}
	case <-time.After(time.Second):
		guest.Close()
		<-done
		t.Fatal("Guest command ignored its timeout because forwarding lacks socket deadlines")
	}
}

func TestGuestCommandTimeoutRetainsPrivateProgressOnForwardedConnection(t *testing.T) {
	port, _ := sshServer(t, "VirtualMac2,1")
	manager, lease := testImageGuest(t, port)
	guest, err := manager.Connect(context.Background(), lease, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	guest.raw = withoutDeadlines{guest.raw}
	out, err := guest.RunReader(context.Background(), time.Second, "progress-block", strings.NewReader(""))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Blocked command lost its timeout", err)
	}
	if out != "Credential isolation phase: rotate-account-password\n" {
		t.Fatal("Timeout discarded the last private mutation phase", out)
	}
}

func TestGuestHandshakeTimeoutWorksWithoutSocketDeadlines(t *testing.T) {
	previous := handshakeTimeout
	handshakeTimeout = 30 * time.Millisecond
	defer func() { handshakeTimeout = previous }()
	client, server := net.Pipe()
	defer server.Close()
	manager, lease := testImageGuest(t, "22")
	manager.Dial = func(context.Context, domain.Lease, string) (net.Conn, error) {
		return withoutDeadlines{client}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.Connect(context.Background(), lease, "127.0.0.1", true)
		done <- err
	}()
	select {
	case err := <-done:
		var failure *domain.Error
		if !errors.As(err, &failure) || failure.Code != "guest_connect_failed" {
			t.Fatal("Cold forwarded banner timeout was treated as a credential failure", err)
		}
	case <-time.After(time.Second):
		client.Close()
		<-done
		t.Fatal("Forwarded handshake ignored its timeout")
	}
}
