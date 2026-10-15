package guestssh

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/qualify"
	"golang.org/x/crypto/ssh"
)

// Revalidate only reads existing credentials and guest state. It must never call
// Prepare, create a missing credential, adopt a host pin or repair guest policy.
// Private-key possession is verified by Control's request proof; SSH's actual
// public-key enquiry additionally verifies that this pinned server admits it.
func (e *Manager) Revalidate(ctx context.Context, l domain.Lease) error {
	if !domain.ValidName(l.ID) || !domain.ValidName(l.ImageID) || l.SSH == nil || l.SSH.User != guestUser(l) || l.SSH.ClientKeyFingerprint == "" {
		return domain.Err("guest_identity_failed", "Revalidation needs the existing VM SSH identity")
	}
	scoped := e.derive("leases")
	c, err := LoadCredentials(scoped.CredentialPath(l))
	if err != nil {
		return domain.Err("lease_credentials_missing", "Existing lease credentials are unavailable; nothing was created")
	}
	if !c.Secured || len(c.HostKey) == 0 {
		return domain.Err("guest_identity_failed", "Existing lease host pin is unavailable")
	}
	pin, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l.SSH.HostKey))
	if err != nil || subtle.ConstantTimeCompare(pin.Marshal(), c.HostKey) != 1 {
		return domain.Err("guest_identity_failed", "Credential host pin differs from the lease manifest")
	}
	caller, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l.SSHPublicKey))
	if err != nil || caller.Type() != ssh.KeyAlgoED25519 || ssh.FingerprintSHA256(caller) != l.SSH.ClientKeyFingerprint {
		return domain.Err("guest_identity_failed", "Caller key differs from the lease manifest")
	}
	management, err := ssh.ParsePrivateKey(c.Private)
	if err != nil {
		return domain.Err("lease_credentials_missing", "Existing management key is invalid")
	}
	g, err := scoped.connectCredentials(ctx, l, l.IP, false, c)
	if err != nil {
		return err
	}
	defer g.Close()
	input := strings.Join([]string{guestUser(l), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(management.PublicKey()))), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(caller)))}, "\n") + "\n"
	out, err := g.Run(ctx, "/bin/bash -c '"+strings.ReplaceAll(revalidateIdentityScript, "'", "'\"'\"'")+"'", input)
	if err != nil || strings.TrimSpace(out) != "virfield-existing-identity-ok" {
		return domain.Err("guest_identity_failed", "Existing guest account or authorized keys changed; no repair was attempted")
	}
	policy, err := g.Run(ctx, "sudo -S -p '' /usr/sbin/sshd -T", c.Password+"\n")
	if err != nil {
		return domain.Err("guest_identity_failed", "Cannot verify existing guest SSH policy")
	}
	for _, setting := range []string{"passwordauthentication no", "kbdinteractiveauthentication no", "permitrootlogin no", "allowagentforwarding no", "hostkey /etc/ssh/ssh_host_ed25519_key"} {
		if !strings.Contains("\n"+policy, "\n"+setting+"\n") {
			return domain.Err("guest_identity_failed", "Existing SSH policy changed; no repair was attempted")
		}
	}
	if l.Source == nil || l.Source.Image == nil {
		return domain.Err("guest_identity_failed", "Original guest profile is unavailable")
	}
	profile := l.Source.Image
	if _, err := qualify.OS(ctx, g, profile.MacOS, profile.Build); err != nil {
		return err
	}
	if _, err := qualify.SIP(ctx, g, profile.DisableSIP); err != nil {
		return err
	}
	if err := qualify.Desktop(ctx, g); err != nil {
		return err
	}
	return scoped.callerKeyAccepted(ctx, l, caller, c.HostKey)
}

const revalidateIdentityScript = `set -eu
set +x
IFS= read -r user
IFS= read -r management
IFS= read -r caller
[ "$(/usr/bin/id -un)" = "$user" ]
[ -d "$HOME/.ssh" ] && [ ! -L "$HOME/.ssh" ]
[ -f "$HOME/.ssh/authorized_keys" ] && [ ! -L "$HOME/.ssh/authorized_keys" ]
[ "$(/usr/bin/stat -f %u "$HOME/.ssh")" = "$(/usr/bin/id -u)" ]
[ "$(/usr/bin/stat -f %u "$HOME/.ssh/authorized_keys")" = "$(/usr/bin/id -u)" ]
[ "$(/usr/bin/stat -f %Lp "$HOME/.ssh")" = 700 ]
[ "$(/usr/bin/stat -f %Lp "$HOME/.ssh/authorized_keys")" = 600 ]
[ "$(/usr/bin/awk 'NF {n++} END {print n+0}' "$HOME/.ssh/authorized_keys")" = 2 ]
/usr/bin/grep -Fxq "$management" "$HOME/.ssh/authorized_keys"
/usr/bin/grep -Fxq "$caller" "$HOME/.ssh/authorized_keys"
printf 'virfield-existing-identity-ok\n'
`

var errKeyAccepted = errors.New("caller public-key enquiry accepted; private key remains local")

type enquirySigner struct {
	public   ssh.PublicKey
	accepted atomic.Bool
}

func (s *enquirySigner) PublicKey() ssh.PublicKey { return s.public }
func (s *enquirySigner) Sign(io.Reader, []byte) (*ssh.Signature, error) {
	s.accepted.Store(true)
	return nil, errKeyAccepted
}

func (e *Manager) callerKeyAccepted(ctx context.Context, l domain.Lease, caller ssh.PublicKey, pin []byte) error {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	address := net.JoinHostPort(l.IP, e.port(l))
	raw, err := e.dial(ctx, l, address)
	if err != nil {
		return domain.Err("caller_authentication_failed", "Cannot verify caller key at the candidate SSH endpoint")
	}
	defer raw.Close()
	deadline, _ := ctx.Deadline()
	_ = raw.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	probe := &enquirySigner{public: caller}
	var hostVerified atomic.Bool
	config := &ssh.ClientConfig{User: l.SSH.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(probe)}, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, Timeout: 10 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if subtle.ConstantTimeCompare(key.Marshal(), pin) != 1 {
			return domain.Err("guest_identity_failed", "Candidate SSH host key changed")
		}
		hostVerified.Store(true)
		return nil
	}}
	conn, _, _, _ := ssh.NewClientConn(raw, address, config)
	if conn != nil {
		_ = conn.Close()
	}
	// Go x/crypto/ssh invokes Sign only after validateKey gets PK_OK. A none-auth
	// success or an accepted TCP connection is not evidence of caller-key policy.
	if ctx.Err() != nil || !hostVerified.Load() || !probe.accepted.Load() {
		return domain.Err("caller_authentication_failed", "Pinned SSH server did not admit the existing caller public key")
	}
	return nil
}
