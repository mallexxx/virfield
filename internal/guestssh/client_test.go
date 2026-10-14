package guestssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

func sshServer(t *testing.T, model string) (string, ssh.Signer) {
	return sshServerFor(t, model, "lume")
}

func sshServerFor(t *testing.T, model, user string) (string, ssh.Signer) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if c.User() != user || string(password) != user {
			return nil, io.EOF
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer raw.Close()
				conn, channels, reqs, err := ssh.NewServerConn(raw, cfg)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range channels {
					channel, requests, err := ch.Accept()
					if err != nil {
						return
					}
					go func() {
						defer channel.Close()
						for req := range requests {
							if req.Type != "exec" {
								req.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							if ssh.Unmarshal(req.Payload, &payload) != nil {
								req.Reply(false, nil)
								return
							}
							req.Reply(true, nil)
							if payload.Command == "slow" {
								time.Sleep(1200 * time.Millisecond)
							}
							if payload.Command == "block" {
								_ = conn.Wait()
								return
							}
							out := model + "\n"
							if payload.Command == "large" {
								out = strings.Repeat("x", 100000)
							}
							_, _ = io.WriteString(channel, out)
							status := make([]byte, 4)
							binary.BigEndian.PutUint32(status, 0)
							_, _ = channel.SendRequest("exit-status", false, status)
							return
						}
					}()
				}
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port, signer
}
func testImageGuest(t *testing.T, port string) (*Manager, domain.Lease) {
	t.Helper()
	e := &Manager{Dir: t.TempDir(), SSHPort: port}
	l := domain.Lease{ID: "image-test", VMName: "golden", Location: "home", Purpose: "image"}
	if err := os.MkdirAll(filepath.Join(e.Dir, "images", l.ID), 0700); err != nil {
		t.Fatal(err)
	}
	return e, l
}
func TestGuestPinsKeyBoundsOutputAndCancels(t *testing.T) {
	port, _ := sshServer(t, "VirtualMac2,1")
	e, l := testImageGuest(t, port)
	ctx := context.Background()
	g, err := e.Connect(ctx, l, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	out, err := g.Run(ctx, "large", "")
	if err != nil || len(out) != 65536 {
		t.Fatal(len(out), err)
	}
	c, err := e.CredentialsFor(l)
	if err != nil || len(c.HostKey) == 0 {
		t.Fatal(c, err)
	}
	info, err := os.Stat(e.CredentialPath(l))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	cancelCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := g.Run(cancelCtx, "block", ""); err == nil || time.Since(started) > time.Second {
		t.Fatal("SSH cancellation is not bounded", err)
	}
	otherPort, _ := sshServer(t, "VirtualMac2,1")
	e.SSHPort = otherPort
	if _, err := e.Connect(ctx, l, "127.0.0.1", true); err == nil {
		t.Fatal("accepted different guest host key")
	}
}
func TestGuestRefusesPhysicalMac(t *testing.T) {
	port, _ := sshServer(t, "Mac15,14")
	e, l := testImageGuest(t, port)
	if _, err := e.Connect(context.Background(), l, "127.0.0.1", true); err == nil {
		t.Fatal("accepted host hardware for privileged image provisioning")
	}
}

func TestVerifiedConnectionNeverCreatesMissingCredentials(t *testing.T) {
	e, l := testImageGuest(t, "22")
	if _, err := e.Connect(context.Background(), l, "127.0.0.1", false); err == nil {
		t.Fatal("missing credential accepted")
	}
	if _, err := os.Stat(e.CredentialPath(l)); !os.IsNotExist(err) {
		t.Fatal("verified connection created credentials", err)
	}
	if _, err := e.CredentialsFor(l); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(e.CredentialPath(l), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Connect(context.Background(), l, "127.0.0.1", false); err == nil {
		t.Fatal("publicly readable credential accepted")
	}
}

func TestForgetImageScopesDeletionAndIsIdempotent(t *testing.T) {
	m := &Manager{Dir: t.TempDir()}
	l := domain.Lease{ID: "image-deleted", Purpose: "image"}
	folder := filepath.Join(m.Dir, "images", l.ID)
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CredentialsFor(l); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []domain.Lease{{ID: "image-deleted"}, {ID: "../images", Purpose: "image"}, {ID: "lease-other", Purpose: "image"}} {
		if err := m.ForgetImage(invalid); err == nil {
			t.Fatal("accepted invalid image identity")
		}
	}
	if _, err := os.Stat(m.CredentialPath(l)); err != nil {
		t.Fatal("invalid request deleted credentials", err)
	}
	for range 2 {
		if err := m.ForgetImage(l); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatal("image data retained", err)
	}
}

func TestBootstrapUsesTemplateGuestUser(t *testing.T) {
	port, _ := sshServerFor(t, "VirtualMac2,1", "admin")
	e, l := testImageGuest(t, port)
	l.GuestUser = "admin"
	g, err := e.Connect(context.Background(), l, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
}

func TestRecordsWithoutGuestUserRemainLume(t *testing.T) {
	port, _ := sshServerFor(t, "VirtualMac2,1", "admin")
	e, l := testImageGuest(t, port)
	if _, err := e.Connect(context.Background(), l, "127.0.0.1", true); err == nil {
		t.Fatal("record without guest user authenticated as admin")
	}
	if guestUser(domain.Lease{}) != "lume" || guestUser(domain.Lease{GuestUser: "admin"}) != "admin" {
		t.Fatal("unexpected guest user default")
	}
}

func TestInvalidGuestUserIsRefused(t *testing.T) {
	e, l := testImageGuest(t, "22")
	l.GuestUser = "admin'; rm -rf /"
	_, err := e.Connect(context.Background(), l, "127.0.0.1", true)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "guest_identity_failed" {
		t.Fatal("accepted invalid guest user", err)
	}
}

func TestConnectUsesLeasePortAndDialer(t *testing.T) {
	port, _ := sshServerFor(t, "iMacPro1,1", "admin")
	dir := t.TempDir()
	var dialed string
	e := &Manager{Dir: dir, Models: []string{"VirtualMac", "iMacPro1,1"}, Dial: func(ctx context.Context, l domain.Lease, address string) (net.Conn, error) {
		dialed = address
		return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+port)
	}}
	l := domain.Lease{ID: "image-test", VMName: "golden", Location: "htpc", Purpose: "image", GuestUser: "admin", SSHPort: 22101}
	if err := os.MkdirAll(filepath.Join(dir, "images", l.ID), 0700); err != nil {
		t.Fatal(err)
	}
	g, err := e.Connect(context.Background(), l, "192.168.1.86", true)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	if dialed != "192.168.1.86:22101" {
		t.Fatal(dialed)
	}
}

func TestConnectRejectsUnlistedGuestModel(t *testing.T) {
	port, _ := sshServerFor(t, "iMacPro1,1", "admin")
	e, l := testImageGuest(t, port)
	l.GuestUser = "admin"
	_, err := e.Connect(context.Background(), l, "127.0.0.1", true)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "guest_identity_failed" {
		t.Fatal(err)
	}
}

// A port forward accepts TCP before the guest's sshd listens; the dropped
// handshake means "not ready yet", not an authentication failure.
func TestDroppedHandshakeIsRetryable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	e, l := testImageGuest(t, port)
	l.GuestUser = "admin"
	_, err = e.Connect(context.Background(), l, "127.0.0.1", true)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "guest_connect_failed" {
		t.Fatal(err)
	}
}

func TestBannerTimeoutIsRetryableWithoutAuthenticationFallback(t *testing.T) {
	previous := handshakeTimeout
	handshakeTimeout = 30 * time.Millisecond
	defer func() { handshakeTimeout = previous }()
	client, server := net.Pipe()
	defer server.Close()
	e, l := testImageGuest(t, "22")
	e.Dial = func(context.Context, domain.Lease, string) (net.Conn, error) { return client, nil }
	_, err := e.Connect(context.Background(), l, "127.0.0.1", true)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "guest_connect_failed" {
		t.Fatal("A cold guest's banner timeout was treated as a credential failure", err)
	}
}

// Credential and SSH-policy scripts can take minutes on a slow guest; probes
// keep their short bound.
func TestMutationsOutliveTheProbeTimeout(t *testing.T) {
	probe, mutation := commandTimeout, mutationTimeout
	commandTimeout, mutationTimeout = 500*time.Millisecond, 5*time.Second
	defer func() { commandTimeout, mutationTimeout = probe, mutation }()
	port, _ := sshServer(t, "VirtualMac2,1")
	e, l := testImageGuest(t, port)
	g, err := e.Connect(context.Background(), l, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if _, err := g.Run(context.Background(), "slow", ""); err == nil {
		t.Fatal("probe outlived its bound")
	}
	// A timed-out probe ends its connection; mutations use a fresh one.
	g, err = e.Connect(context.Background(), l, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if _, err := g.RunMutation(context.Background(), "slow", ""); err != nil {
		t.Fatal("mutation cut short:", err)
	}
}
