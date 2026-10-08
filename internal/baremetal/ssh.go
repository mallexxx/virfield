package baremetal

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

// Prepare authorizes the lease's client key on the node and returns the node
// account with its pinned host key. The host key is not rotated per lease: a
// physical host keeps its identity.
func (r *Runtime) Prepare(ctx context.Context, l domain.Lease) (domain.SSHConnection, error) {
	var none domain.SSHConnection
	public, err := domain.CanonicalPublicKey(l.SSHPublicKey)
	if err != nil {
		return none, err
	}
	if !strings.HasPrefix(l.VMName, "vf-") || !domain.ValidName(l.VMName) {
		return none, domain.Err("invalid_request", "Not a disposable lease name")
	}
	n, err := r.node(l.Location)
	if err != nil {
		return none, err
	}
	host, _, _, _, err := ssh.ParseAuthorizedKey([]byte(n.HostKey))
	if err != nil {
		return none, domain.Err("guest_identity_failed", "Node host key is not configured")
	}
	client, _, _, _, err := ssh.ParseAuthorizedKey([]byte(public))
	if err != nil {
		return none, err
	}
	s, err := r.dial(ctx, n)
	if err != nil {
		return none, err
	}
	defer s.Close()
	out, err := s.Run(ctx, command(prepareScript, l.VMName), public+"\n")
	if err != nil || strings.TrimSpace(out) != "1" {
		return none, domain.Err("lease_ssh_failed", "Node did not authorize exactly one lease key")
	}
	return domain.SSHConnection{User: n.User, Port: 22, HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host))), ClientKeyFingerprint: ssh.FingerprintSHA256(client)}, nil
}

// SSHDialer authenticates with Virfield's node management key and accepts
// only the node's configured host key.
func SSHDialer(signer ssh.Signer) dialer {
	return func(ctx context.Context, n Node) (session, error) {
		want, _, _, _, err := ssh.ParseAuthorizedKey([]byte(n.HostKey))
		if err != nil {
			return nil, errors.New("node host key is invalid")
		}
		cfg := &ssh.ClientConfig{User: n.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(want), HostKeyAlgorithms: []string{want.Type()}, Timeout: 8 * time.Second}
		dialer := net.Dialer{Timeout: 8 * time.Second}
		raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(n.Address, "22"))
		if err != nil {
			return nil, err
		}
		deadline := time.Now().Add(10 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = raw.SetDeadline(deadline)
		conn, chans, reqs, err := ssh.NewClientConn(raw, raw.RemoteAddr().String(), cfg)
		if err != nil {
			raw.Close()
			return nil, err
		}
		_ = raw.SetDeadline(time.Time{})
		return &sshSession{client: ssh.NewClient(conn, chans, reqs)}, nil
	}
}

type sshSession struct{ client *ssh.Client }

func (s *sshSession) Close() { _ = s.client.Close() }

// Run executes one fixed command; output is bounded and stays private.
func (s *sshSession) Run(ctx context.Context, command, input string) (string, error) {
	session, err := s.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var out boundedOutput
	session.Stdin = strings.NewReader(input)
	session.Stdout = &out
	session.Stderr = &out
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	case err := <-done:
		return out.String(), err
	}
}

type boundedOutput struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := 65536 - b.b.Len(); room > 0 {
		b.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *boundedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

var _ io.Writer = (*boundedOutput)(nil)
