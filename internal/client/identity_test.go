package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

func TestIdentityAndPinnedConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lease identity")
	if err := GenerateIdentity(dir); err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(filepath.Join(dir, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if err := GenerateIdentity(dir); err == nil {
		t.Fatal("overwrote identity")
	}
	l := domain.Lease{ID: "lease-test", State: "ready", IP: "192.168.64.10", SSH: &domain.SSHConnection{User: "lume", Port: 22, HostKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey())), ClientKeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey())}}
	wrong := l
	copySSH := *l.SSH
	copySSH.ClientKeyFingerprint = "wrong"
	wrong.SSH = &copySSH
	if err := WriteSSHConfig(dir, wrong); err == nil {
		t.Fatal("accepted wrong client identity")
	}
	if err := WriteSSHConfig(dir, l); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"StrictHostKeyChecking yes", "HostKeyAlias lease-test", "IdentityAgent none", "ControlPath none"} {
		if !strings.Contains(string(config), want) {
			t.Fatal("missing", want)
		}
	}
	if err := WriteSSHConfig(dir, l); err == nil {
		t.Fatal("overwrote host pin")
	}
	if strings.Contains(string(config), "PRIVATE KEY") {
		t.Fatal("private key exported")
	}
}

func TestPinnedConfigUsesLeasePort(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "node vm")
	if err := GenerateIdentity(dir); err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(filepath.Join(dir, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	host := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	l := domain.Lease{ID: "lease-vm", State: "ready", IP: "192.0.2.20", SSHPort: 22101, SSH: &domain.SSHConnection{User: "admin", Port: 22101, HostKey: host, ClientKeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey())}}
	if err := WriteSSHConfig(dir, l); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(dir, "config"))
	known, _ := os.ReadFile(filepath.Join(dir, "known_hosts"))
	if !strings.Contains(string(config), "  HostName 192.0.2.20\n") || !strings.Contains(string(config), "  Port 22101\n") {
		t.Fatal(string(config))
	}
	if !strings.HasPrefix(string(known), "lease-vm ssh-ed25519 ") {
		t.Fatal(string(known))
	}
}
