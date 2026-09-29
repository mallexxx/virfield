package domain

import (
	"crypto/ed25519"
	"crypto/rand"
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
