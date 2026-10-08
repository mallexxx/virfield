package baremetal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func authorized(s ssh.Signer) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey())))
}

// serveSSH accepts handshakes with one host key until the test ends.
func serveSSH(t *testing.T, host ssh.Signer) string {
	t.Helper()
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				conn, chans, reqs, err := ssh.NewServerConn(raw, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for c := range chans {
					c.Reject(ssh.Prohibited, "test")
				}
				conn.Close()
			}()
		}
	}()
	return listener.Addr().String()
}

// A node with several boot volumes presents a different host key per volume.
func TestSSHDialerAcceptsAnyConfiguredHostKey(t *testing.T) {
	monterey, bigSur, stranger := newSigner(t), newSigner(t), newSigner(t)
	host, port, _ := net.SplitHostPort(serveSSH(t, bigSur))
	dial := sshDialer(newSigner(t), port)
	node := Node{ID: "mbp-2014", Address: host, User: "admin", HostKeys: []string{authorized(monterey), authorized(bigSur)}}
	s, err := dial(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.HostKey() != authorized(bigSur) {
		t.Fatalf("presented host key = %q", s.HostKey())
	}
	node.HostKeys = []string{authorized(monterey), authorized(stranger)}
	if s, err := dial(context.Background(), node); err == nil {
		s.Close()
		t.Fatal("accepted an unconfigured host key")
	}
}
