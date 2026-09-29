package guestssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

// Prepare replaces every inherited authentication secret before a lease is ready.
// The caller owns its private key; only its validated public key enters this job.
func (e *Manager) Prepare(ctx context.Context, l domain.Lease) (domain.SSHConnection, error) {
	var result domain.SSHConnection
	if !domain.ValidName(l.ID) || !domain.ValidName(l.ImageID) || l.SSHPublicKey == "" {
		return result, domain.Err("ssh_profile_missing", "Lease requires a verified image identity and client public key")
	}
	public, err := domain.CanonicalPublicKey(l.SSHPublicKey)
	if err != nil {
		return result, err
	}
	source := &Manager{Dir: e.Dir, SSHPort: e.SSHPort}
	image := domain.Lease{ID: l.ImageID}
	// Do not bootstrap a missing source credential or trust a new host key.
	if _, err := os.Stat(source.CredentialPath(image)); err != nil {
		return result, domain.Err("image_credentials_missing", "Verified image credentials are unavailable")
	}
	inherited, err := LoadCredentials(source.CredentialPath(image))
	if err != nil {
		return result, err
	}
	if !inherited.Secured || len(inherited.HostKey) == 0 {
		return result, domain.Err("image_credentials_missing", "Image credentials are not verified")
	}
	g, err := source.ConnectReady(ctx, image, l.IP, false)
	if err != nil {
		return result, err
	}

	defer g.Close()
	scoped := &Manager{Dir: e.Dir, Scope: "leases", SSHPort: e.SSHPort}
	if err := os.MkdirAll(scoped.Directory(l), 0700); err != nil {
		return result, err
	}
	c, err := scoped.CredentialsFor(l)
	if err != nil {
		return result, err
	}
	if len(c.HostPrivate) == 0 {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return result, err
		}
		block, err := ssh.MarshalPrivateKey(key, "")
		if err != nil {
			return result, err
		}
		signer, err := ssh.NewSignerFromKey(key)
		if err != nil {
			return result, err
		}
		c.HostPrivate = pem.EncodeToMemory(block)
		c.HostKey = signer.PublicKey().Marshal()
	}
	signer, err := ssh.ParsePrivateKey(c.Private)
	if err != nil {
		return result, err
	}
	management := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	// Persist all identities before sending any mutation. Interrupted provisioning
	// is held for cleanup by the controller; there is no blind bootstrap retry.
	c.Secured = true
	if err := SaveCredentials(scoped.CredentialPath(l), c); err != nil {
		return result, err
	}
	input := strings.Join([]string{inherited.Password, c.Password, management, public, base64.StdEncoding.EncodeToString(c.HostPrivate)}, "\n") + "\n"
	out, err := g.Run(ctx, "/bin/bash -c '"+strings.ReplaceAll(leaseScript, "'", "'\"'\"'")+"'", input)
	if err != nil {
		if saveErr := os.WriteFile(filepath.Join(scoped.Directory(l), "provision.log"), []byte(out), 0600); saveErr != nil {
			return result, saveErr
		}
		return result, domain.Err("lease_ssh_failed", "Lease credential isolation failed; inspect the private provision log and release this lease")
	}
	// The new daemon key must authenticate against the new host key. Inspect the
	// effective daemon policy and exact authorized keys, not merely an open port.
	verified, err := scoped.Connect(ctx, l, l.IP, false)
	if err != nil {
		return result, err
	}
	defer verified.Close()
	keys, err := verified.Run(ctx, "/bin/cat ~/.ssh/authorized_keys", "")
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(keys) != management+"\n"+public {
		return result, domain.Err("lease_ssh_failed", "Guest authorized keys differ from the lease manifest")
	}
	policy, err := verified.Run(ctx, "sudo -S -p '' /usr/sbin/sshd -T", c.Password+"\n")
	if err != nil {
		return result, err
	}
	for _, setting := range []string{"passwordauthentication no", "kbdinteractiveauthentication no", "permitrootlogin no", "allowagentforwarding no", "hostkey /etc/ssh/ssh_host_ed25519_key"} {
		if !strings.Contains("\n"+policy, "\n"+setting+"\n") {
			return result, domain.Err("lease_ssh_failed", "Guest SSH policy differs from the lease policy")
		}
	}
	// Verify revocation using the NEW host pin, so host-key rotation alone
	// cannot masquerade as refusal of the old authentication key.
	inherited.HostKey = c.HostKey
	old, err := scoped.connectCredentials(ctx, l, l.IP, false, inherited)
	if err == nil {
		old.Close()
		return result, domain.Err("lease_ssh_failed", "Image SSH identity was not revoked")
	}
	var rejection *domain.Error
	if !errors.As(err, &rejection) || rejection.Code != "guest_authentication_failed" {
		return result, domain.Err("lease_ssh_failed", "Could not verify rejection of the inherited image key")
	}
	host, _ := ssh.ParsePublicKey(c.HostKey)
	clientKey, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(public))
	return domain.SSHConnection{User: "lume", Port: 22, HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host))), ClientKeyFingerprint: ssh.FingerprintSHA256(clientKey)}, nil
}

// Fixed script, only generated secrets and canonical public keys arrive on stdin.
// No script, path or host shell is supplied by an API client.
const leaseScript = `set -eu
IFS= read -r old
IFS= read -r new
IFS= read -r management
IFS= read -r client
IFS= read -r hostkey
umask 077
mkdir -p ~/.ssh ~/workspace
chmod 700 ~/.ssh ~/workspace
/usr/sbin/sysadminctl -newPassword "$new" -oldPassword "$old"
/usr/bin/dscl . -authonly lume "$new" >/dev/null 2>&1
printf '%s\n' "$new" | sudo -S -p '' /usr/sbin/sysadminctl -autologin set -userName lume -password "$new" -adminUser lume -adminPassword "$new"
printf '%s' "$hostkey" | /usr/bin/base64 -D > ~/.ssh/virfield-host-key
printf '%s\n' "$new" | sudo -S -p '' /usr/bin/install -m 600 ~/.ssh/virfield-host-key /etc/ssh/ssh_host_ed25519_key
/bin/rm ~/.ssh/virfield-host-key
printf '%s\n' "$new" | sudo -S -p '' /bin/sh -c '/bin/rm -f /etc/ssh/ssh_host_rsa_key /etc/ssh/ssh_host_rsa_key.pub /etc/ssh/ssh_host_ecdsa_key /etc/ssh/ssh_host_ecdsa_key.pub; /usr/bin/ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key > /etc/ssh/ssh_host_ed25519_key.pub; printf "HostKey /etc/ssh/ssh_host_ed25519_key\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin no\nAllowAgentForwarding no\n" > /etc/ssh/sshd_config.d/000-virfield.conf; chmod 600 /etc/ssh/sshd_config.d/000-virfield.conf; /usr/sbin/sshd -t'
printf '%s\n%s\n' "$management" "$client" > ~/.ssh/authorized_keys.next
chmod 600 ~/.ssh/authorized_keys.next
mv ~/.ssh/authorized_keys.next ~/.ssh/authorized_keys
`

// Forget removes only daemon-generated files of a confirmed absent disposable lease.
func (e *Manager) Forget(l domain.Lease) error {
	if !strings.HasPrefix(l.ID, "lease-") || !domain.ValidName(l.ID) || l.Purpose == "image" {
		return domain.Err("invalid_request", "Not a disposable lease")
	}
	return os.RemoveAll(filepath.Join(e.Dir, "leases", l.ID))
}
