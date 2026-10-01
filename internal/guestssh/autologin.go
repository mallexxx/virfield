package guestssh

import "encoding/base64"

// Older macOS lacks sysadminctl -autologin. loginwindow reads a NUL-terminated
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
macos=$(/usr/bin/sw_vers -productVersion)
major=${macos%%.*}
if [ "$major" -ge 13 ]; then
 printf '%s\n' "$new" | sudo -S -p '' /usr/sbin/sysadminctl -autologin set -userName lume -password "$new" -adminUser lume -adminPassword "$new"
else
 umask 077
 printf '%s' "$kcpassword" | /usr/bin/base64 -D > ~/.ssh/virfield-kcpassword
 printf '%s\n' "$new" | sudo -S -p '' /usr/bin/install -o root -g wheel -m 600 ~/.ssh/virfield-kcpassword /etc/kcpassword
 /bin/rm ~/.ssh/virfield-kcpassword
 printf '%s\n' "$new" | sudo -S -p '' /usr/bin/defaults write /Library/Preferences/com.apple.loginwindow autoLoginUser -string lume
fi
`
