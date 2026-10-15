package guestssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/qualify"
	"golang.org/x/crypto/ssh"
)

func TestRevalidationNeverCreatesMissingCredentials(t *testing.T) {
	m := &Manager{Dir: t.TempDir()}
	l := domain.Lease{ID: "lease-existing", ImageID: "image-existing", GuestUser: "admin", SSH: &domain.SSHConnection{User: "admin", ClientKeyFingerprint: "existing-fingerprint"}}
	err := m.Revalidate(context.Background(), l)
	var fault *domain.Error
	if !errors.As(err, &fault) || fault.Code != "lease_credentials_missing" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Dir, "leases")); !os.IsNotExist(err) {
		t.Fatal("Readonly revalidation created a credential directory", err)
	}
}

func TestCallerKeyEnquiryCancellationClosesBlockedTransport(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	client, peer := net.Pipe()
	defer peer.Close()
	m := &Manager{Dial: func(context.Context, domain.Lease, string) (net.Conn, error) { return client, nil }}
	l := domain.Lease{IP: "127.0.0.1", SSH: &domain.SSHConnection{User: "admin"}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = m.callerKeyAccepted(ctx, l, key, key.Marshal())
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("Caller enquiry ignored canceled context", err)
	}
}

func TestCallerPublicKeyEnquiryRequiresActualAcceptanceAndOriginalHostPin(t *testing.T) {
	for _, mode := range []string{"accepted", "rejected", "wrong-host", "none-auth"} {
		t.Run(mode, func(t *testing.T) {
			_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			host, err := ssh.NewSignerFromKey(hostPrivate)
			if err != nil {
				t.Fatal(err)
			}
			caller, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			pub, err := ssh.NewPublicKey(caller)
			if err != nil {
				t.Fatal(err)
			}
			var queried atomic.Bool
			config := &ssh.ServerConfig{NoClientAuth: mode == "none-auth", PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
				queried.Store(true)
				if mode == "rejected" || metadata.User() != "admin" || string(key.Marshal()) != string(pub.Marshal()) {
					return nil, errors.New("key refused")
				}
				return nil, nil
			}}
			config.AddHostKey(host)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				connection, _, _, _ := ssh.NewServerConn(raw, config)
				if connection != nil {
					_ = connection.Close()
				}
			}()
			manager := &Manager{}
			ip, portText, _ := net.SplitHostPort(listener.Addr().String())
			address := listener.Addr().(*net.TCPAddr)
			_ = portText
			l := domain.Lease{ID: "lease-probe", IP: ip, SSHPort: address.Port, SSH: &domain.SSHConnection{User: "admin"}}
			pin := host.PublicKey().Marshal()
			if mode == "wrong-host" {
				pin = pub.Marshal()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = manager.callerKeyAccepted(ctx, l, pub, pin)
			if mode == "accepted" {
				if err != nil || !queried.Load() {
					t.Fatal("Admitted key was not independently queried", err)
				}
			} else if err == nil {
				t.Fatal("False caller authentication acceptance", mode)
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("Enquiry did not terminate")
			}
		})
	}
}

func TestRevalidateReadsExistingGuestPolicyWithoutChangingCredentials(t *testing.T) {
	for _, mode := range []string{"accepted", "keys-changed", "policy-changed"} {
		t.Run(mode, func(t *testing.T) {
			newSigner := func() (ssh.Signer, ed25519.PrivateKey) {
				t.Helper()
				_, key, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				s, err := ssh.NewSignerFromKey(key)
				if err != nil {
					t.Fatal(err)
				}
				return s, key
			}
			host, _ := newSigner()
			management, managementPrivate := newSigner()
			caller, _ := newSigner()
			var queried atomic.Bool
			commands := make(chan string, 16)
			config := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
				if metadata.User() != "admin" {
					return nil, errors.New("wrong user")
				}
				if string(key.Marshal()) == string(management.PublicKey().Marshal()) {
					return nil, nil
				}
				if string(key.Marshal()) == string(caller.PublicKey().Marshal()) {
					queried.Store(true)
					return nil, nil
				}
				return nil, errors.New("unknown key")
			}}
			config.AddHostKey(host)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				for {
					raw, err := listener.Accept()
					if err != nil {
						return
					}
					go func() {
						defer raw.Close()
						conn, channels, requests, err := ssh.NewServerConn(raw, config)
						if err != nil {
							return
						}
						defer conn.Close()
						go ssh.DiscardRequests(requests)
						for request := range channels {
							if request.ChannelType() != "session" {
								_ = request.Reject(ssh.Prohibited, "sessions only")
								continue
							}
							channel, messages, err := request.Accept()
							if err != nil {
								return
							}
							go func() {
								defer channel.Close()
								for message := range messages {
									if message.Type != "exec" {
										_ = message.Reply(false, nil)
										continue
									}
									var payload struct{ Command string }
									if ssh.Unmarshal(message.Payload, &payload) != nil {
										_ = message.Reply(false, nil)
										return
									}
									_ = message.Reply(true, nil)
									commands <- payload.Command
									_, _ = io.ReadAll(io.LimitReader(channel, 8192))
									output := ""
									status := uint32(0)
									switch {
									case payload.Command == "/usr/sbin/sysctl -n hw.model":
										output = "VirtualMac2,1\n"
									case payload.Command == "/usr/bin/sw_vers -buildVersion":
										output = "21H1320\n"
									case payload.Command == "/usr/bin/sw_vers -productVersion":
										output = "12.7.6\n"
									case payload.Command == "/usr/bin/csrutil status":
										output = "System Integrity Protection status: enabled.\n"
									case payload.Command == qualify.DesktopProbe:
										output = "desktop\n"
									case strings.Contains(payload.Command, "virfield-existing-identity-ok"):
										output = "virfield-existing-identity-ok\n"
										if mode == "keys-changed" {
											output = "keys-changed\n"
										}
									case payload.Command == "sudo -S -p '' /usr/sbin/sshd -T":
										output = "passwordauthentication no\nkbdinteractiveauthentication no\npermitrootlogin no\nallowagentforwarding no\nhostkey /etc/ssh/ssh_host_ed25519_key\n"
										if mode == "policy-changed" {
											output = strings.ReplaceAll(output, "passwordauthentication no", "passwordauthentication yes")
										}
									default:
										status = 1
									}
									_, _ = io.WriteString(channel, output)
									_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
									return
								}
							}()
						}
					}()
				}
			}()
			key, err := ssh.MarshalPrivateKey(managementPrivate, "")
			if err != nil {
				t.Fatal(err)
			}
			m := &Manager{Dir: t.TempDir()}
			address := listener.Addr().(*net.TCPAddr)
			l := domain.Lease{ID: "lease-readonly", ImageID: "image-original", GuestUser: "admin", IP: "127.0.0.1", SSHPort: address.Port, SSHPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(caller.PublicKey()))), SSH: &domain.SSHConnection{User: "admin", Port: address.Port, HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host.PublicKey()))), ClientKeyFingerprint: ssh.FingerprintSHA256(caller.PublicKey())}}
			l.Source = &domain.Template{Image: &domain.ImageProfile{MacOS: "12.7.6", Build: "21H1320"}}
			scoped := m.derive("leases")
			if err := os.MkdirAll(scoped.Directory(l), 0700); err != nil {
				t.Fatal(err)
			}
			credentials := Credentials{Secured: true, Password: "admin", Private: pem.EncodeToMemory(key), HostKey: host.PublicKey().Marshal()}
			if err := SaveCredentials(scoped.CredentialPath(l), credentials); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(scoped.CredentialPath(l))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			err = m.Revalidate(ctx, l)
			if mode == "accepted" {
				if err != nil || !queried.Load() {
					t.Fatal("Existing policy/key not verified", err)
				}
			} else if err == nil || queried.Load() {
				t.Fatal("Changed policy continued to caller acceptance", mode, err)
			}
			after, err := os.ReadFile(scoped.CredentialPath(l))
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("Revalidation rewrote private credentials")
			}
			if _, err := os.Stat(filepath.Join(m.Dir, "images")); !os.IsNotExist(err) {
				t.Fatal("Revalidation touched image credentials")
			}
			for len(commands) > 0 {
				command := <-commands
				for _, forbidden := range []string{"sysadminctl", "dscl", "ssh-keygen", "mkdir", "install ", "security unlock"} {
					if strings.Contains(command, forbidden) {
						t.Fatal("Revalidation attempted provisioning", forbidden)
					}
				}
			}
		})
	}
}
