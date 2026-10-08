package baremetal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
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

// serveForwarding accepts direct-tcpip channels, records the requested
// target and connects it to target instead.
func serveForwarding(t *testing.T, host ssh.Signer, target string) (string, *string) {
	t.Helper()
	requested := new(string)
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
				_, chans, reqs, err := ssh.NewServerConn(raw, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for c := range chans {
					var p struct {
						Host       string
						Port       uint32
						OriginHost string
						OriginPort uint32
					}
					if c.ChannelType() != "direct-tcpip" || ssh.Unmarshal(c.ExtraData(), &p) != nil {
						c.Reject(ssh.Prohibited, "test")
						continue
					}
					*requested = net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port)))
					ch, reqs, err := c.Accept()
					if err != nil {
						continue
					}
					go ssh.DiscardRequests(reqs)
					out, err := net.Dial("tcp", target)
					if err != nil {
						ch.Close()
						continue
					}
					go func() { io.Copy(out, ch); out.Close() }()
					go func() { io.Copy(ch, out); ch.Close() }()
				}
			}()
		}
	}()
	return listener.Addr().String(), requested
}

func TestDialGuestTunnelsToNodeLoopbackPort(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	host := newSigner(t)
	addr, requested := serveForwarding(t, host, echo.Addr().String())
	address, port, _ := net.SplitHostPort(addr)
	n := Node{ID: "htpc", Address: address, User: "bazzite", HostKeys: []string{authorized(host)}, OS: "linux", Arch: "amd64"}
	r := New([]Node{n}, sshDialer(newSigner(t), port))
	conn, err := r.DialGuest(context.Background(), domain.Lease{Location: "htpc", SSHPort: 22100}, "192.168.1.86:22100")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatal(string(buf), err)
	}
	if *requested != "127.0.0.1:22100" {
		t.Fatal("tunnel target", *requested)
	}
}

func TestDialServiceDesktopTunnelsToNodeLoopback(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	host := newSigner(t)
	addr, requested := serveForwarding(t, host, echo.Addr().String())
	address, port, _ := net.SplitHostPort(addr)
	n := Node{ID: "htpc", Address: address, User: "bazzite", HostKeys: []string{authorized(host)}, OS: "linux", Arch: "amd64", VMs: &VMConfig{PortBase: 22100, Max: 2}}
	r := New([]Node{n}, sshDialer(newSigner(t), port))
	l := domain.Lease{Location: "htpc", SSHPort: 22100, ImageID: "image-x"}
	conn, err := r.DialService(context.Background(), l, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if *requested != "127.0.0.1:23100" {
		t.Fatal("desktop target", *requested)
	}
	if _, err := r.DialService(context.Background(), l, "ssh"); err == nil {
		t.Fatal("dialed a service other than desktop")
	}
	if _, err := r.DialService(context.Background(), domain.Lease{Location: "htpc"}, "desktop"); err == nil {
		t.Fatal("dialed a desktop for a host lease")
	}
}
