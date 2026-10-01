package guestssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

// Manager owns private guest credentials and a fixed, bounded SSH transport.
// Scope is chosen by the daemon, never by API callers.
type Manager struct{ Dir, Scope, SSHPort string }

func (e *Manager) Directory(l domain.Lease) string {
	scope := e.Scope
	if scope == "" {
		scope = "images"
	}
	return filepath.Join(e.Dir, scope, l.ID)
}

type Credentials struct {
	HostPrivate []byte `json:"host_private_key,omitempty"`
	Private     []byte `json:"private_key"`
	Password    string `json:"password"`
	HostKey     []byte `json:"host_key,omitempty"`
	Secured     bool   `json:"secured"`
}
type Client struct {
	client *ssh.Client
	raw    net.Conn
}

func (g *Client) Close() { _ = g.client.Close(); _ = g.raw.Close() }
func (e *Manager) CredentialPath(l domain.Lease) string {
	return filepath.Join(e.Directory(l), "credentials.json")
}
func SaveCredentials(path string, c Credentials) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".credentials-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (e *Manager) CredentialsFor(l domain.Lease) (Credentials, error) {
	path := e.CredentialPath(l)
	c, err := LoadCredentials(path)
	if err == nil {
		return c, nil
	}
	if !os.IsNotExist(err) {
		return Credentials{}, err
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Credentials{}, err
	}
	key, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		return Credentials{}, err
	}
	c = Credentials{Private: pem.EncodeToMemory(key), Password: domain.NewID("vf-")}
	return c, SaveCredentials(path, c)
}
func (e *Manager) Connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (*Client, error) {
	c, err := LoadCredentials(e.CredentialPath(l))
	if bootstrap && os.IsNotExist(err) {
		c, err = e.CredentialsFor(l)
	}
	if err != nil {
		return nil, err
	}
	if !bootstrap && (len(c.HostKey) == 0 || !c.Secured) {
		return nil, domain.Err("guest_identity_failed", "Guest credential has no verified identity")
	}
	return e.connectCredentials(ctx, l, ip, bootstrap, c)
}

// ConnectReady retries only failed TCP connections, before any guest command or
// credential mutation. macOS can temporarily deny the first local-network dial
// after a daemon update; a Lume SSH-ready flag is not an authenticated connection.
// Host-key and authentication failures are never retried or downgraded.
func (e *Manager) ConnectReady(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return waitForConnection(ctx, 2*time.Second, func() (*Client, error) {
		return e.Connect(ctx, l, ip, bootstrap)
	})
}

func waitForConnection(ctx context.Context, delay time.Duration, connect func() (*Client, error)) (*Client, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g, err := connect()
		var failure *domain.Error
		if err == nil || !errors.As(err, &failure) || failure.Code != "guest_connect_failed" {
			return g, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func (e *Manager) connectCredentials(ctx context.Context, l domain.Lease, ip string, bootstrap bool, c Credentials) (*Client, error) {
	signer, err := ssh.ParsePrivateKey(c.Private)
	if err != nil {
		return nil, err
	}
	auth := []ssh.AuthMethod{ssh.PublicKeys(signer)}
	// Bootstrap is allowed only inside the new-image pipeline, never as a
	// connection fallback for arbitrary templates or leased execution VMs.
	if bootstrap && !c.Secured {
		auth = append(auth, ssh.Password("lume"))
	}
	cfg := &ssh.ClientConfig{User: "lume", Auth: auth, Timeout: 10 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		actual := key.Marshal()
		if len(c.HostKey) == 0 {
			c.HostKey = append([]byte(nil), actual...)
			return SaveCredentials(e.CredentialPath(l), c)
		}
		if subtle.ConstantTimeCompare(actual, c.HostKey) != 1 {
			return fmt.Errorf("guest host key changed")
		}
		return nil
	}}
	if e.Scope == "leases" {
		cfg.HostKeyAlgorithms = []string{ssh.KeyAlgoED25519}
	}
	port := e.SSHPort
	if port == "" {
		port = "22"
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
	if err != nil {
		_ = os.WriteFile(filepath.Join(e.Directory(l), "ssh-connect.log"), []byte(err.Error()+"\n"), 0600)
		return nil, domain.Err("guest_connect_failed", "Cannot connect to the guest SSH endpoint; inspect private ssh-connect.log")
	}
	handshakeDeadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(handshakeDeadline) {
		handshakeDeadline = d
	}
	_ = raw.SetDeadline(handshakeDeadline)
	stopCancellation := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stopCancellation()
	conn, chans, reqs, err := ssh.NewClientConn(raw, net.JoinHostPort(ip, port), cfg)
	if err != nil {
		raw.Close()
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, domain.Err("guest_authentication_failed", "Guest rejected the SSH credential")
		}
		return nil, domain.Err("guest_ssh_failed", "Guest authentication or pinned host key verification failed")
	}
	_ = raw.SetDeadline(time.Time{})
	g := &Client{client: ssh.NewClient(conn, chans, reqs), raw: raw}
	model, err := g.Run(ctx, "/usr/sbin/sysctl -n hw.model", "")
	if err != nil || !strings.Contains(model, "VirtualMac") {
		g.Close()
		return nil, domain.Err("guest_identity_failed", "Refusing provisioning: target is not an Apple virtual Mac")
	}
	return g, nil
}
func (g *Client) Run(ctx context.Context, command, input string) (string, error) {
	return g.RunReader(ctx, time.Minute, command, strings.NewReader(input))
}

func (g *Client) RunReader(ctx context.Context, timeout time.Duration, command string, input io.Reader) (string, error) {
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = g.raw.SetDeadline(deadline)
	defer func() { _ = g.raw.SetDeadline(time.Time{}) }()
	session, err := g.client.NewSession()
	if err != nil {
		return "", domain.Err("guest_command_failed", "Cannot open guest SSH session")
	}
	defer session.Close()
	session.Stdin = input
	// Fixed commands return only short probes. Bound even a compromised guest's output.
	var out boundedOutput
	session.Stdout = &out
	session.Stderr = &out
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case <-ctx.Done():
		g.Close()
		<-done
		return "", ctx.Err()
	case err := <-done:
		if err != nil {
			// Output stays private to the image executor; never attach it to a
			// public error, event or API response.
			return out.String(), domain.Err("guest_command_failed", "Fixed guest provisioning command failed")
		}
	}
	return out.String(), nil
}

type boundedOutput struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.b.Len() < 65536 {
		_, _ = b.b.Write(p[:min(n, 65536-b.b.Len())])
	}
	return n, nil
}
func (b *boundedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
func (e *Manager) SecureImage(ctx context.Context, l domain.Lease, ip string, g *Client) error {
	c, err := e.CredentialsFor(l)
	if err != nil {
		return err
	}
	signer, err := ssh.ParsePrivateKey(c.Private)
	if err != nil {
		return err
	}
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	// Values are generated internally (hex and OpenSSH base64), never supplied by
	// an API caller. No credentials or arbitrary host commands enter this script.
	command := "umask 077; mkdir -p ~/.ssh ~/workspace; printf '%s\\n' '" + pub + " virfield-image' > ~/.ssh/authorized_keys; chmod 700 ~/.ssh ~/workspace; chmod 600 ~/.ssh/authorized_keys"
	if _, err := g.Run(ctx, command, ""); err != nil {
		return err
	}
	// The administrator password travels over encrypted SSH stdin. Shell variables
	// are used only inside the guest; no secret appears in host argv or event logs.
	runSecret := func(stage, script, input string) error {
		out, err := g.Run(ctx, "/bin/bash -c '"+strings.ReplaceAll(script, "'", "'\"'\"'")+"'", input)
		if err != nil {
			if logErr := os.WriteFile(filepath.Join(e.Directory(l), "credential-"+stage+".log"), []byte(out), 0600); logErr != nil {
				return logErr
			}
			return domain.Err("credential_"+stage+"_failed", "Guest credential "+stage+" failed; inspect the private stage log")
		}
		return nil
	}
	old := "lume"
	if c.Secured {
		old = c.Password
	}
	rotate := `IFS= read -r old
IFS= read -r new
if ! /usr/bin/dscl . -authonly lume "$new" >/dev/null 2>&1; then
 /usr/sbin/sysadminctl -newPassword "$new" -oldPassword "$old" || exit 1
fi
/usr/bin/dscl . -authonly lume "$new" >/dev/null 2>&1
`
	if err := runSecret("rotation", rotate, old+"\n"+c.Password+"\n"); err != nil {
		return err
	}
	autologin := `set -eu
IFS= read -r new
IFS= read -r kcpassword
` + autologinScript
	if err := runSecret("autologin", autologin, c.Password+"\n"+loginPassword(c.Password)+"\n"); err != nil {
		return err
	}
	if err := VerifyAutoLogin(ctx, g, c.Password); err != nil {
		return err
	}
	harden := `IFS= read -r new
printf '%s\n' "$new" | sudo -S -p '' /bin/sh -c 'umask 077; mkdir -p /etc/ssh/sshd_config.d; printf "PasswordAuthentication no\nKbdInteractiveAuthentication no\nChallengeResponseAuthentication no\n" > /etc/ssh/sshd_config.d/000-virfield.conf; /usr/sbin/sshd -t'
`
	if err := runSecret("ssh", harden, c.Password+"\n"); err != nil {
		return err
	}
	effective, err := g.Run(ctx, "sudo -S -p '' /usr/sbin/sshd -T", c.Password+"\n")
	if err != nil || !strings.Contains("\n"+effective, "\npasswordauthentication no\n") || !strings.Contains("\n"+effective, "\nkbdinteractiveauthentication no\n") {
		return domain.Err("credential_verification_failed", "Guest SSH still permits password authentication; image will not be published")
	}
	c.Secured = true
	if err := SaveCredentials(e.CredentialPath(l), c); err != nil {
		return err
	}

	verified, err := e.Connect(ctx, l, ip, false)
	if err != nil {
		return err
	}
	defer verified.Close()
	out, err := verified.Run(ctx, "test -d ~/workspace && /usr/bin/stat -f %Lp ~/.ssh/authorized_keys", "")
	if err != nil || strings.TrimSpace(out) != "600" {
		return domain.Err("credential_verification_failed", "Scoped image SSH verification failed")
	}
	return nil
}

// LoadCredentials never creates or repairs missing credentials during a verified connection.
func LoadCredentials(path string) (Credentials, error) {
	var c Credentials
	info, err := os.Lstat(path)
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 32768 {
		return c, fmt.Errorf("credential file must be an owner-only regular file, at most 32 KiB")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Password == "" || strings.ContainsAny(c.Password, "\r\n") {
		return c, errors.New("invalid guest credential")
	}
	if _, err = ssh.ParsePrivateKey(c.Private); err != nil {
		return c, errors.New("invalid guest private key")
	}
	if len(c.HostKey) > 0 {
		if _, err = ssh.ParsePublicKey(c.HostKey); err != nil {
			return c, errors.New("invalid guest host pin")
		}
	}
	if c.Secured && len(c.HostKey) == 0 {
		return c, errors.New("verified credential has no host pin")
	}
	return c, nil
}
