package guestssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
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
		if c.User() != "lume" || string(password) != "lume" {
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
