package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

// GenerateIdentity creates a new, owner-only directory. It never discovers,
// uploads or overwrites an existing private key.
func GenerateIdentity(dir string) error {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(private, "virfield-lease")
	if err != nil {
		return err
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519"), pem.EncodeToMemory(block), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "id_ed25519.pub"), ssh.MarshalAuthorizedKey(signer.PublicKey()), 0600)
}

// WriteSSHConfig binds the locally generated identity to the authenticated
// lease host key. No shell is invoked and existing connection files are refused.
func WriteSSHConfig(dir string, l domain.Lease) error {
	dir, knownContent, configContent, err := sshConnectionFiles(dir, l)
	if err != nil {
		return err
	}
	knownPath := filepath.Join(dir, "known_hosts")
	if err := writeNew(knownPath, knownContent); err != nil {
		return err
	}
	err = writeNew(filepath.Join(dir, "config"), configContent)
	if err != nil {
		_ = os.Remove(knownPath)
	}
	return err
}

func sshConnectionFiles(dir string, l domain.Lease) (string, string, string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", "", err
	}
	// OpenSSH expands tokens in paths even inside quotes. Refuse those paths.
	if strings.ContainsAny(dir, "\r\n\"\\%$") {
		return "", "", "", fmt.Errorf("SSH identity path contains unsupported characters")
	}
	if l.State != "ready" || l.SSH == nil || net.ParseIP(l.IP) == nil || l.SSH.User != "lume" || l.SSH.Port != 22 || !domain.ValidName(l.ID) {
		return "", "", "", fmt.Errorf("lease has no verified SSH connection")
	}
	host, err := domain.CanonicalPublicKey(l.SSH.HostKey)
	if err != nil {
		return "", "", "", err
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	info, err := os.Lstat(keyPath)
	if err != nil {
		return "", "", "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", "", "", fmt.Errorf("private key must be an owner-only regular file")
	}
	b, err := os.ReadFile(keyPath)
	if err != nil {
		return "", "", "", err
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return "", "", "", err
	}
	if ssh.FingerprintSHA256(signer.PublicKey()) != l.SSH.ClientKeyFingerprint {
		return "", "", "", fmt.Errorf("local private key does not belong to this lease")
	}
	knownPath := filepath.Join(dir, "known_hosts")
	knownContent := l.ID + " " + host + "\n"
	configContent := fmt.Sprintf("Host virfield\n  HostName %s\n  User lume\n  Port 22\n  HostKeyAlias %s\n  IdentityFile \"%s\"\n  UserKnownHostsFile \"%s\"\n  GlobalKnownHostsFile /dev/null\n  StrictHostKeyChecking yes\n  IdentitiesOnly yes\n  IdentityAgent none\n  ForwardAgent no\n  PasswordAuthentication no\n  KbdInteractiveAuthentication no\n  ControlMaster no\n  ControlPath none\n  ConnectTimeout 10\n", l.IP, l.ID, keyPath, knownPath)
	return dir, knownContent, configContent, nil
}

func writeNew(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(content)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
