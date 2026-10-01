package guestssh

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// Headless SSH cannot reliably use sysadminctl -autologin (Sequoia returns
// SACSetAutoLoginPassword error:22 with exit status zero). loginwindow reads a NUL-terminated
// password XOR-obfuscated with Apple's fixed key. This is a credential, not
// encryption: it travels only over SSH stdin and is installed root-only.
func loginPassword(password string) string {
	key := []byte{0x7d, 0x89, 0x52, 0x23, 0xd2, 0xbc, 0xdd, 0xea, 0xa3, 0xb9, 0x1f}
	size := ((len(password) + 1 + len(key) - 1) / len(key)) * len(key)
	b := make([]byte, size)
	copy(b, password)
	for i := range b {
		b[i] ^= key[i%len(key)]
	}
	return base64.StdEncoding.EncodeToString(b)
}

// new and kcpassword are read from the private stdin by both callers.
const autologinScript = `
umask 077
printf '%s' "$kcpassword" | /usr/bin/base64 -D > ~/.ssh/virfield-kcpassword
printf '%s\n' "$new" | sudo -S -p '' /usr/bin/install -o root -g wheel -m 600 ~/.ssh/virfield-kcpassword /etc/kcpassword
/bin/rm ~/.ssh/virfield-kcpassword
printf '%s\n' "$new" | sudo -S -p '' /usr/bin/defaults write /Library/Preferences/com.apple.loginwindow autoLoginUser -string lume

`

// VerifyAutoLogin reads the private cache over pinned SSH and never returns its
// contents. sysadminctl can exit successfully without changing the stored secret.
func VerifyAutoLogin(ctx context.Context, g *Client, password string) error {
	out, err := g.Run(ctx, `test "$(/usr/bin/defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser)" = lume && sudo -S -p '' /bin/sh -c 'test "$(/usr/bin/stat -f %u:%g:%Lp /etc/kcpassword)" = 0:0:600 && /usr/bin/base64 -i /etc/kcpassword'`, password+"\n")
	if err != nil || !matchesLoginPassword(out, password) {
		return domain.Err("credential_autologin_failed", "Guest automatic login does not contain the current private password; image or lease will not be published")
	}
	return nil
}

func matchesLoginPassword(encoded, password string) bool {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(b) == 0 || len(b)%11 != 0 {
		return false
	}
	key := []byte{0x7d, 0x89, 0x52, 0x23, 0xd2, 0xbc, 0xdd, 0xea, 0xa3, 0xb9, 0x1f}
	for i := range b {
		b[i] ^= key[i%len(key)]
	}
	end := bytes.IndexByte(b, 0)
	return end >= 0 && string(b[:end]) == password
}

// BootstrapAutoLogin is confined to a fresh guest's native Setup Assistant.
// Credential rotation remains in the subsequent journaled assistant stage.
func BootstrapAutoLogin(ctx context.Context, g *Client) error {
	script := `set -eu
IFS= read -r new
IFS= read -r kcpassword
umask 077
mkdir -p ~/.ssh
chmod 700 ~/.ssh
` + autologinScript
	_, err := g.Run(ctx, "/bin/bash -c '"+strings.ReplaceAll(script, "'", "'\"'\"'")+"'", "lume\n"+loginPassword("lume")+"\n")
	if err != nil {
		return err
	}
	return VerifyAutoLogin(ctx, g, "lume")
}
