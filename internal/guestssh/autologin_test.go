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
	for _, script := range []string{autologinScript, leaseScript} {
		cmd := exec.Command("/bin/bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid credential script: %s: %v", out, err)
		}
	}
}
