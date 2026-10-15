package guestssh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

func TestLoginPasswordTerminatesAndPads(t *testing.T) {
	key := []byte{0x7d, 0x89, 0x52, 0x23, 0xd2, 0xbc, 0xdd, 0xea, 0xa3, 0xb9, 0x1f}
	for _, password := range []string{"", "lume", "12345678901", "0123456789abcdef0123456789abcdef"} {
		b, err := base64.StdEncoding.DecodeString(loginPassword(password))
		if err != nil || len(b)%11 != 0 {
			t.Fatal(err, len(b))
		}
		for i := range b {
			b[i] ^= key[i%11]
		}
		if !bytes.Equal(b[:len(password)], []byte(password)) || b[len(password)] != 0 {
			t.Fatal("password not NUL terminated")
		}
	}
}

func TestCredentialScriptsParse(t *testing.T) {
	for _, script := range []string{autologinScript, leaseScript, standardAccountScript} {
		cmd := exec.Command("/bin/bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid credential script: %s: %v", out, err)
		}
	}
}

func TestCredentialScriptsUseConfiguredGuestUser(t *testing.T) {
	for name, script := range map[string]string{"autologin": autologinScript, "lease": leaseScript, "account": standardAccountScript} {
		if strings.Contains(script, "lume") || !strings.Contains(script, `"$user"`) {
			t.Fatalf("%s script does not use the configured guest user", name)
		}
	}
	for name, script := range map[string]string{"lease": leaseScript, "account": standardAccountScript} {
		if !strings.Contains(script, "IFS= read -r user\n") {
			t.Fatalf("%s script must read the guest user first", name)
		}
	}
}

func TestAutologinRejectsStaleSecretAndAcceptsNativeRandomPadding(t *testing.T) {
	if !matchesLoginPassword(loginPassword("rotated-password"), "rotated-password") || matchesLoginPassword(loginPassword("lume"), "rotated-password") || matchesLoginPassword("invalid", "") {
		t.Fatal("incorrect autologin verification")
	}
	b, _ := base64.StdEncoding.DecodeString(loginPassword("lume"))
	// Native kcpassword may contain random padding after the NUL terminator.
	b[len(b)-1] ^= 0x5a
	if !matchesLoginPassword(base64.StdEncoding.EncodeToString(b), "lume") {
		t.Fatal("valid native padding rejected")
	}
}

// Each VM lease signs in to Screen Sharing with its standard account, so the
// lease script enables the service and Prepare verifies its real RFB greeting.
func TestLeaseScriptEnablesScreenSharing(t *testing.T) {
	for _, want := range []string{
		`printf '%s\n' "$new" | sudo -S -p '' /bin/launchctl enable system/com.apple.screensharing`,
		`printf '%s\n' "$new" | sudo -S -p '' /bin/launchctl bootstrap system /System/Library/LaunchDaemons/com.apple.screensharing.plist 2>/dev/null || true`,
	} {
		if !strings.Contains(leaseScript, want) {
			t.Fatal("lease script lacks", want)
		}
	}
}

func TestLeasePasswordReadsTheLeaseCredential(t *testing.T) {
	m := &Manager{Dir: t.TempDir()}
	l := domain.Lease{ID: "lease-0123456789abcdef"}
	if _, err := m.LeasePassword(l); err == nil {
		t.Fatal("password without a lease credential")
	}
	scoped := m.derive("leases")
	if err := os.MkdirAll(scoped.Directory(l), 0700); err != nil {
		t.Fatal(err)
	}
	c, err := scoped.CredentialsFor(l)
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	c.Secured, c.HostKey = true, host.Marshal()
	if err := SaveCredentials(scoped.CredentialPath(l), c); err != nil {
		t.Fatal(err)
	}
	if got, err := m.LeasePassword(l); err != nil || got != c.Password {
		t.Fatal(got, err)
	}
}
