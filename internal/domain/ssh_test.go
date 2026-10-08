package domain

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestCanonicalPublicKey(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	value := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	got, err := CanonicalPublicKey(value + " comment\n")
	if err != nil || got != value {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"", "not a key", "command=\"id\" " + value, value + "\n" + value, strings.Repeat("x", 1025)} {
		if _, err := CanonicalPublicKey(bad); err == nil {
			t.Fatal("accepted invalid key")
		}
	}
}

func TestLeaseAndVMCarrySSHPort(t *testing.T) {
	b, _ := json.Marshal(VM{Name: "vf-a", IP: "192.168.1.86", SSHPort: 22101})
	if !strings.Contains(string(b), `"ssh_port":22101`) {
		t.Fatal(string(b))
	}
	b, _ = json.Marshal(Lease{ID: "lease-a", IP: "192.168.1.86", SSHPort: 22101})
	if !strings.Contains(string(b), `"ssh_port":22101`) {
		t.Fatal(string(b))
	}
	b, _ = json.Marshal(VM{Name: "vf-b", IP: "10.0.0.2"})
	if strings.Contains(string(b), "ssh_port") {
		t.Fatal("port 0 must be omitted so existing leases are unchanged")
	}
}
