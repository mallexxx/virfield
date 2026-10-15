package guestssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/qualify"
	"golang.org/x/crypto/ssh"
)

// A verified image can expose SSH while loginwindow is still starting. Neither
// credential rotation nor a secured worker identity may precede GUI readiness.
func TestPrepareDoesNotRotateBeforeInheritedDesktop(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var mutation, desktop atomic.Bool
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		conn, channels, requests, err := ssh.NewServerConn(raw, cfg)
		if err != nil {
			return
		}
		defer conn.Close()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			channel, requests, err := incoming.Accept()
			if err != nil {
				return
			}
			for request := range requests {
				var payload struct{ Command string }
				if request.Type != "exec" || ssh.Unmarshal(request.Payload, &payload) != nil {
					_ = request.Reply(false, nil)
					continue
				}
				_ = request.Reply(true, nil)
				out, status := "", uint32(0)
				switch payload.Command {
				case "/usr/sbin/sysctl -n hw.model":
					out = "VirtualMac2,1\n"
				case qualify.DesktopProbe:
					desktop.Store(true)
					out = "waiting\n"
				default:
					mutation.Store(strings.Contains(payload.Command, "sysadminctl"))
					status = 1
				}
				_, _ = io.WriteString(channel, out)
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				_ = channel.Close()
				break
			}
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	m := &Manager{Dir: t.TempDir(), SSHPort: port}
	image := domain.Lease{ID: "image-startup", GuestUser: "admin"}
	if err := os.MkdirAll(m.Directory(image), 0700); err != nil {
		t.Fatal(err)
	}
	c, err := m.CredentialsFor(image)
	if err != nil {
		t.Fatal(err)
	}
	c.Secured, c.HostKey = true, host.PublicKey().Marshal()
	if err := SaveCredentials(m.CredentialPath(image), c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.CredentialPath(image))
	if err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{ID: "lease-startup", ImageID: image.ID, GuestUser: "admin", IP: "127.0.0.1", SSHPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host.PublicKey())))}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := m.Prepare(ctx, l); err == nil {
		t.Fatal("Prepared a worker while inherited desktop was absent")
	}
	<-serverDone
	if mutation.Load() || !desktop.Load() {
		t.Fatal("Credential mutation preceded inherited desktop readiness")
	}
	if _, err := os.Stat(m.derive("leases").CredentialPath(l)); !os.IsNotExist(err) {
		t.Fatal("Created a secured worker identity before read-only GUI preflight", err)
	}
	after, err := os.ReadFile(m.CredentialPath(image))
	if err != nil || string(before) != string(after) {
		t.Fatal("Read-only preflight changed the image identity", err)
	}
}
