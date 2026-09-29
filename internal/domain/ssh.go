package domain

import (
	"strings"

	"golang.org/x/crypto/ssh"
)

// CanonicalPublicKey accepts one plain Ed25519 public key, without authorized_keys
// options or certificates. Comments are discarded before fingerprinting requests.
func CanonicalPublicKey(value string) (string, error) {
	if len(value) > 1024 {
		return "", Err("invalid_request", "SSH key must be one Ed25519 public key")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return "", Err("invalid_request", "SSH key must be one plain Ed25519 public key without options or certificates")
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}

type SSHConnection struct {
	User                 string `json:"user"`
	Port                 int    `json:"port"`
	HostKey              string `json:"host_key"`
	ClientKeyFingerprint string `json:"client_key_fingerprint"`
}
