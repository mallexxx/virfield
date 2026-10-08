package baremetal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// LoadOrCreateKey returns Virfield's node management key, creating it once.
// The private key must stay owner-only; the public key is written beside it
// for enrollment on each node account.
func LoadOrCreateKey(path string) (ssh.Signer, string, error) {
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, "", errors.New("node management key must be an owner-only regular file")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		signer, err := ssh.ParsePrivateKey(b)
		if err != nil {
			return nil, "", err
		}
		return signer, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
	} else if !os.IsNotExist(err) {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, "", err
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	block, err := ssh.MarshalPrivateKey(private, "virfield-node-management")
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		return nil, "", err
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return nil, "", err
	}
	public := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if err := os.WriteFile(path+".pub", []byte(public+" virfield-node-management\n"), 0644); err != nil {
		return nil, "", err
	}
	return signer, public, nil
}
