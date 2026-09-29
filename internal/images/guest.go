package images

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

type credentials struct {
	Private  []byte `json:"private_key"`
	Password string `json:"password"`
	HostKey  []byte `json:"host_key,omitempty"`
	Secured  bool   `json:"secured"`
}
type guest struct {
	client *ssh.Client
	raw    net.Conn
}

func (g *guest) Close() { _ = g.client.Close(); _ = g.raw.Close() }
func (e *Engine) credentialPath(l domain.Lease) string {
	return filepath.Join(e.Dir, "images", l.ID, "credentials.json")
}
func saveCredentials(path string, c credentials) error {
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
func (e *Engine) credentials(l domain.Lease) (credentials, error) {
	path := e.credentialPath(l)
	b, err := os.ReadFile(path)
	if err == nil {
		var c credentials
		err = json.Unmarshal(b, &c)
		return c, err
	}
	if !os.IsNotExist(err) {
		return credentials{}, err
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return credentials{}, err
	}
	key, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		return credentials{}, err
	}
	c := credentials{Private: pem.EncodeToMemory(key), Password: domain.NewID("vf-")}
	return c, saveCredentials(path, c)
}
func (e *Engine) connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (*guest, error) {
	c, err := e.credentials(l)
	if err != nil {
		return nil, err
	}
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
			return saveCredentials(e.credentialPath(l), c)
		}
		if subtle.ConstantTimeCompare(actual, c.HostKey) != 1 {
			return fmt.Errorf("guest host key changed")
		}
		return nil
	}}
	port := e.sshPort
	if port == "" {
		port = "22"
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
	if err != nil {
		return nil, domain.Err("guest_ssh_failed", "Cannot connect to the image SSH endpoint")
	}
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	conn, chans, reqs, err := ssh.NewClientConn(raw, net.JoinHostPort(ip, port), cfg)
	if err != nil {
		raw.Close()
		return nil, domain.Err("guest_ssh_failed", "Guest authentication or pinned host key verification failed")
	}
	_ = raw.SetDeadline(time.Time{})
	g := &guest{client: ssh.NewClient(conn, chans, reqs), raw: raw}
	model, err := g.run(ctx, "/usr/sbin/sysctl -n hw.model", "")
	if err != nil || !strings.Contains(model, "VirtualMac") {
		g.Close()
		return nil, domain.Err("guest_identity_failed", "Refusing provisioning: target is not an Apple virtual Mac")
	}
	return g, nil
}
func (g *guest) run(ctx context.Context, command, input string) (string, error) {
	deadline := time.Now().Add(60 * time.Second)
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
	session.Stdin = strings.NewReader(input)
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
func (e *Engine) secure(ctx context.Context, l domain.Lease, ip string, g *guest) error {
	c, err := e.credentials(l)
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
	if _, err := g.run(ctx, command, ""); err != nil {
		return err
	}
	// The administrator password travels over encrypted SSH stdin. Shell variables
	// are used only inside the guest; no secret appears in host argv or event logs.
	runSecret := func(stage, script, input string) error {
		out, err := g.run(ctx, "/bin/bash -c '"+strings.ReplaceAll(script, "'", "'\"'\"'")+"'", input)
		if err != nil {
			if logErr := os.WriteFile(filepath.Join(e.Dir, "images", l.ID, "credential-"+stage+".log"), []byte(out), 0600); logErr != nil {
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
	autologin := `IFS= read -r new
printf '%s\n' "$new" | sudo -S -p '' /usr/sbin/sysadminctl -autologin set -userName lume -password "$new" -adminUser lume -adminPassword "$new"
`
	if err := runSecret("autologin", autologin, c.Password+"\n"); err != nil {
		return err
	}
	harden := `IFS= read -r new
printf '%s\n' "$new" | sudo -S -p '' /bin/sh -c 'umask 077; mkdir -p /etc/ssh/sshd_config.d; printf "PasswordAuthentication no\nKbdInteractiveAuthentication no\n" > /etc/ssh/sshd_config.d/000-virfield.conf; /usr/sbin/sshd -t'
`
	if err := runSecret("ssh", harden, c.Password+"\n"); err != nil {
		return err
	}
	effective, err := g.run(ctx, "sudo -S -p '' /usr/sbin/sshd -T", c.Password+"\n")
	if err != nil || !strings.Contains("\n"+effective, "\npasswordauthentication no\n") || !strings.Contains("\n"+effective, "\nkbdinteractiveauthentication no\n") {
		return domain.Err("credential_verification_failed", "Guest SSH still permits password authentication; image will not be published")
	}
	c.Secured = true
	if err := saveCredentials(e.credentialPath(l), c); err != nil {
		return err
	}

	verified, err := e.connect(ctx, l, ip, false)
	if err != nil {
		return err
	}
	defer verified.Close()
	out, err := verified.run(ctx, "test -d ~/workspace && /usr/bin/stat -f %Lp ~/.ssh/authorized_keys", "")
	if err != nil || strings.TrimSpace(out) != "600" {
		return domain.Err("credential_verification_failed", "Scoped image SSH verification failed")
	}
	return nil
}
