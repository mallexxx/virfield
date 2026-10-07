package guestssh

import (
	"bytes"
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"
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
	for _, script := range []string{autologinScript, leaseScript, rotateScript} {
		cmd := exec.Command("/bin/bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid credential script: %s: %v", out, err)
		}
	}
}

func TestCredentialScriptsUseConfiguredGuestUser(t *testing.T) {
	for name, script := range map[string]string{"autologin": autologinScript, "lease": leaseScript, "rotate": rotateScript} {
		if strings.Contains(script, "lume") || !strings.Contains(script, `"$user"`) {
			t.Fatalf("%s script does not use the configured guest user", name)
		}
	}
	for name, script := range map[string]string{"lease": leaseScript, "rotate": rotateScript} {
		if !strings.HasPrefix(strings.TrimPrefix(script, "set -eu\n"), "IFS= read -r user\n") {
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
