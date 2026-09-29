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
