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
// account with the host key it presented. The host key is not rotated per
// lease: a physical host keeps the identity of the system it booted.
func (r *Runtime) Prepare(ctx context.Context, l domain.Lease) (domain.SSHConnection, error) {
	var none domain.SSHConnection
	// A node VM lease is a cloned image; only host leases have no image.
	if l.ImageID != "" {
		if r.guests == nil {
			return none, domain.Err("ssh_profile_missing", "Node VM leases need a guest credential manager")
		}
		return r.guests.Prepare(ctx, l)
	}
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
	return domain.SSHConnection{User: n.User, Port: 22, HostKey: s.HostKey(), ClientKeyFingerprint: ssh.FingerprintSHA256(client)}, nil
}

// SSHDialer authenticates with Virfield's node management key and accepts
// only the node's configured host keys.
func SSHDialer(signer ssh.Signer) dialer { return sshDialer(signer, "22") }

func sshDialer(signer ssh.Signer, port string) dialer {
	return func(ctx context.Context, n Node) (session, error) {
		accepted, algorithms, err := hostKeys(n)
		if err != nil {
			return nil, err
		}
		var presented string
		check := func(_ string, _ net.Addr, key ssh.PublicKey) error {
			presented = string(key.Marshal())
			if !accepted[presented] {
				return errors.New("node presented an unconfigured host key")
			}
			return nil
		}
		cfg := &ssh.ClientConfig{User: n.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: check, HostKeyAlgorithms: algorithms, Timeout: 4 * time.Second}
		// Nodes share this LAN; a short dial keeps sleeping nodes cheap.
		dialer := net.Dialer{Timeout: 4 * time.Second}
		raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(n.Address, port))
		if err != nil {
			return nil, err
		}
		stop := context.AfterFunc(ctx, func() { raw.Close() })
		defer stop()
		deadline := time.Now().Add(10 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = raw.SetDeadline(deadline)
		conn, chans, reqs, err := ssh.NewClientConn(raw, raw.RemoteAddr().String(), cfg)
		if err != nil {
			raw.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		_ = raw.SetDeadline(time.Time{})
		stop()
		if ctx.Err() != nil {
			raw.Close()
			return nil, ctx.Err()
		}
		key, _ := ssh.ParsePublicKey([]byte(presented))
		host := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		return &sshSession{client: ssh.NewClient(conn, chans, reqs), host: host}, nil
	}
}

// hostKeys indexes a node's identities by wire encoding and lists their
// algorithms so the handshake negotiates a key Virfield can verify.
func hostKeys(n Node) (map[string]bool, []string, error) {
	accepted, seen := map[string]bool{}, map[string]bool{}
	var algorithms []string
	for _, line := range n.HostKeys {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, nil, errors.New("node host key is invalid")
		}
		accepted[string(key.Marshal())] = true
		if !seen[key.Type()] {
			seen[key.Type()] = true
			algorithms = append(algorithms, key.Type())
		}
	}
	if len(accepted) == 0 {
		return nil, nil, errors.New("node host key is not configured")
	}
	return accepted, algorithms, nil
}

type sshSession struct {
	client *ssh.Client
	host   string
}

func (s *sshSession) Close() { _ = s.client.Close() }

func (s *sshSession) HostKey() string { return s.host }

func (s *sshSession) Dial(network, address string) (net.Conn, error) {
	return s.client.Dial(network, address)
}

// Run executes one fixed command; output is bounded and stays private.
func (s *sshSession) Run(ctx context.Context, command, input string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	// NewSession waits for a channel-open reply too. Closing only after Run
	// starts leaves that earlier wait outside the command's context bound.
	stop := context.AfterFunc(ctx, s.Close)
	defer stop()
	session, err := s.client.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	defer session.Close()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
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
