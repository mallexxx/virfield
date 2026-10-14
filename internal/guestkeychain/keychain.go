// Package guestkeychain prepares and verifies a VM keychain in its Aqua session.
package guestkeychain

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestscripts"
)

type Guest interface {
	RunReader(context.Context, time.Duration, string, io.Reader) (string, error)
}

type Proof struct {
	Version        int    `json:"version"`
	Scope          string `json:"scope"`
	Nonce          string `json:"nonce"`
	Boot           string `json:"boot"`
	SessionID      int    `json:"session_id"`
	Aqua           bool   `json:"aqua"`
	Unlocked       bool   `json:"unlocked"`
	AddStatus      int    `json:"add_status"`
	ReadStatus     int    `json:"read_status"`
	DeleteStatus   int    `json:"delete_status"`
	ValueEqual     bool   `json:"value_equal"`
	Ready          bool   `json:"ready"`
	SSHSessionID   int    `json:"ssh_session_id,omitempty"`
	SSHVerified    bool   `json:"ssh_verified"`
	DisplayWidth   int    `json:"display_width,omitempty"`
	DisplayHeight  int    `json:"display_height,omitempty"`
	DisplayManaged bool   `json:"display_managed"`
}

// Ensure installs the private per-image/lease keychain and GUI login agent.
// Existing keychain files are preserved. The password travels only on SSH stdin.
func Ensure(ctx context.Context, g Guest, user, scope, password string, previousPassword ...string) (Proof, error) {
	if password == "" || len(password) > 256 || strings.ContainsAny(password, "\r\n") {
		return Proof{}, failure()
	}
	if len(previousPassword) > 1 {
		return Proof{}, failure()
	}
	previous := ""
	if len(previousPassword) == 1 {
		previous = previousPassword[0]
		if len(previous) > 256 || strings.ContainsAny(previous, "\r\n") {
			return Proof{}, failure()
		}
	}
	return run(ctx, g, "ensure", user, scope, password, previous)
}

// Verify runs a new GUI probe without unlocking the keychain. It requires this
// boot's successful login agent, native WhenUnlocked write/read and probe cleanup.
func Verify(ctx context.Context, g Guest, user, scope string) (Proof, error) {
	return run(ctx, g, "verify", user, scope, "", "")
}

func run(ctx context.Context, g Guest, mode, user, scope, password, previous string) (Proof, error) {
	var proof Proof
	if !domain.ValidName(user) || !domain.ValidName(scope) || !strings.HasPrefix(scope, "image-") && !strings.HasPrefix(scope, "lease-") {
		return proof, failure()
	}
	nonce := domain.NewID("keychain-")
	command := "/bin/bash -c '" + strings.ReplaceAll(guestscripts.Keychain, "'", "'\"'\"'") + "'"
	input := strings.Join([]string{mode, user, scope, password, nonce, previous}, "\n") + "\n"
	out, err := g.RunReader(ctx, 3*time.Minute, command, strings.NewReader(input))
	if err != nil || json.Unmarshal([]byte(out), &proof) != nil || proof.Version != 1 || proof.Scope != scope || proof.Nonce != nonce || !domain.ValidName(proof.Boot) || proof.SessionID < 1 || !proof.Aqua || !proof.Unlocked || !proof.ValueEqual || !proof.Ready || proof.AddStatus != 0 || proof.ReadStatus != 0 || proof.DeleteStatus != 0 || proof.DisplayManaged && (proof.DisplayWidth != 1920 || proof.DisplayHeight != 1080) {
		return reject(ctx, g, "gui", out)
	}
	// A GUI unlock does not unlock a new SSH Security audit session. The fixed
	// sshd bridge unlocks before each command; prove native access in that session
	// too, without handing an unlock password to the verification command.
	sshNonce := domain.NewID("keychain-")
	sshCommand := `/bin/bash -c '# ai.virfield.guest-keychain SSH audit probe
set -eu
IFS= read -r scope
IFS= read -r nonce
# The current provisioning connection predates the new sshd ForceCommand.
# Invoke its exact bridge here too; later fresh SSH connections run it in sshd.
export SSH_ORIGINAL_COMMAND="/usr/bin/osascript -l JavaScript \"\$HOME/.virfield-keychain/probe.js\" \"$scope\" \"$nonce\" \"$(/usr/sbin/sysctl -n kern.bootsessionuuid)\" ssh"
exec /bin/bash "$HOME/.virfield-keychain/ssh-session.sh"'`
	out, err = g.RunReader(ctx, time.Minute, sshCommand, strings.NewReader(scope+"\n"+sshNonce+"\n"))
	var sshProof Proof
	if err != nil || json.Unmarshal([]byte(out), &sshProof) != nil || sshProof.Version != 1 || sshProof.Scope != scope || sshProof.Nonce != sshNonce || sshProof.Boot != proof.Boot || sshProof.SessionID < 1 || !sshProof.Unlocked || !sshProof.ValueEqual || !sshProof.Ready || sshProof.AddStatus != 0 || sshProof.ReadStatus != 0 || sshProof.DeleteStatus != 0 {
		return reject(ctx, g, "ssh", out)
	}
	proof.SSHSessionID, proof.SSHVerified = sshProof.SessionID, true
	return proof, nil
}

func reject(ctx context.Context, g Guest, stage, output string) (Proof, error) {
	// Collect before the provisioning connection closes: a failed GUI setup may
	// leave the new SSH bridge unavailable, even though its host key is pinned.
	if reporter, ok := g.(interface {
		ReportKeychainFailure(context.Context, string, string)
	}); ok {
		reporter.ReportKeychainFailure(ctx, stage, output)
	}
	return Proof{}, failure()
}

func failure() error {
	return domain.Err("guest_keychain_failed", "VM GUI or SSH keychain is unavailable, locked or cannot store/read an application key; image/lease will not be ready")
}
