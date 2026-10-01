package images

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/lume"
	"github.com/mallexxx/virfield/internal/registry"
)

// ExportStep only handles fresh portable builds. A clone of a private managed
// disk is not sufficient: deleted credentials can remain in APFS snapshots and
// free blocks. Portable builds never write a private administrator password.
func (e *Engine) ExportStep(ctx context.Context, l domain.Lease, target domain.RegistryExport, step string) (string, error) {
	if !l.Portable || l.Purpose != "image" || !domain.ValidName(l.ID) || !strings.HasPrefix(l.VMName, "vf-export-") {
		return "", domain.Err("invalid_profile", "Registry export requires a fresh portable build")
	}
	if e.Registry == nil {
		return "", domain.Err("registry_unconfigured", "Registry is not configured")
	}
	if step == "sanitize" {
		return "", e.sanitizeExport(ctx, l)
	}
	if step != "upload" {
		return "", domain.Err("invalid_profile", "Unknown registry export stage")
	}
	organization, err := e.Registry.PushTarget(target.Source)
	if err != nil {
		return "", err
	}
	if organization != target.Organization {
		return "", domain.Err("registry_source_changed", "Registry organization changed after acceptance")
	}
	source, err := e.Registry.Source(target.Source)
	if err != nil {
		return "", err
	}
	if !domain.ValidRegistryRepository(target.Repository) || !domain.ValidRegistryTag(target.Tag) {
		return "", domain.Err("invalid_registry", "Invalid registry target")
	}
	proof, err := os.ReadFile(filepath.Join(e.Dir, "images", l.ID, "portable-sanitized"))
	if err != nil || string(proof) != l.ID {
		return "", domain.Err("registry_unsanitized", "Portable image has no completed sanitation evidence")
	}
	observation, err := e.Backend.Observe(ctx)
	if err != nil {
		return "", err
	}
	stopped := false
	for _, vm := range observation.VMs {
		if vm.Key() == l.Key() {
			stopped = vm.State == "stopped"
		}
	}
	if !stopped {
		return "", domain.Err("image_in_use", "Registry publication requires the sanitized VM to be stopped")
	}
	request := domain.RegistryResolveRequest{Source: target.Source, Repository: target.Repository, Tag: target.Tag}
	_, err = e.Registry.Resolve(ctx, request)
	var failure *domain.Error
	if err == nil {
		return "", domain.Err("registry_tag_exists", "Registry tag appeared before publication; refusing to overwrite it")
	}
	if !errors.As(err, &failure) || failure.Code != "registry_not_found" {
		return "", err
	}
	token, err := registry.Credentials(source)
	if err != nil {
		return "", err
	}
	if err := lume.RegistryPush(ctx, e.Tools.Lume, l, source, target.Repository, target.Tag, token, filepath.Join(e.Dir, "images", l.ID, "registry-push.log"), e.StoragePaths[l.Location]); err != nil {
		return "", err
	}
	ref, err := e.Registry.Resolve(ctx, request)
	if err != nil {
		return "", domain.Err("registry_publish_unknown", "Upload returned but published manifest could not be verified; inspect the target tag before recovery")
	}
	return ref.Digest, nil
}
func (e *Engine) sanitizeExport(ctx context.Context, l domain.Lease) error {
	manager := &guestssh.Manager{Dir: e.Dir}
	credentials, err := guestssh.LoadCredentials(manager.CredentialPath(l))
	if err != nil {
		return err
	}
	if !credentials.Secured || credentials.Password != "lume" {
		return domain.Err("registry_unsanitized", "Export build must use only the public bootstrap password")
	}
	vm, err := e.boot(ctx, l)
	if err != nil {
		return err
	}
	g, err := e.connect(ctx, l, vm.IP, false)
	if err != nil {
		return err
	}
	defer g.Close()
	if _, err := e.verifyDiskPolicy(ctx, l, g); err != nil {
		return err
	}
	const script = `set -euo pipefail
[[ $(/usr/sbin/sysctl -n hw.model) == VirtualMac* ]]
/usr/bin/dscl . -authonly lume lume
# This VM was rebuilt from the pinned recipe, never cloned from a managed disk.
/bin/rm -rf /Users/lume/.ssh /Users/lume/.zsh_sessions /Users/lume/.bash_sessions /var/root/.ssh
/bin/rm -f /Users/lume/.zsh_history /Users/lume/.bash_history /Users/lume/.git-credentials /Users/lume/.netrc
/bin/rm -f /etc/ssh/ssh_host_* /etc/ssh/sshd_config.d/000-virfield.conf
/bin/mkdir -p /etc/ssh/sshd_config.d
/usr/bin/printf 'PasswordAuthentication yes\nKbdInteractiveAuthentication no\nChallengeResponseAuthentication no\nPermitRootLogin no\nAllowAgentForwarding no\n' > /etc/ssh/sshd_config.d/000-virfield.conf
/bin/chmod 600 /etc/ssh/sshd_config.d/000-virfield.conf
[[ ! -e /Users/lume/.ssh && ! -e /var/root/.ssh ]]
! /usr/bin/find /etc/ssh -name 'ssh_host_*' -type f | /usr/bin/grep -q .
`
	if _, err := g.Run(ctx, "sudo -S -p '' /bin/bash -c "+shellQuote(script), "lume\n"); err != nil {
		return domain.Err("registry_unsanitized", "Portable credential sanitation failed; nothing uploaded")
	}
	// Removing SSH keys prevents new sessions. Use this already-authenticated
	// connection for clean shutdown, then require observed stop before upload.
	_, _ = g.Run(ctx, "sudo -S -p '' /sbin/shutdown -h now", "lume\n")
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		observation, err := e.Backend.Observe(wait)
		if err == nil {
			for _, current := range observation.VMs {
				if current.Key() == l.Key() && current.State == "stopped" {
					return os.WriteFile(filepath.Join(e.Dir, "images", l.ID, "portable-sanitized"), []byte(l.ID), 0600)
				}
			}
		}
		if err := pause(wait, time.Second); err != nil {
			return domain.Err("registry_unsanitized", "Portable VM did not confirm clean shutdown; nothing uploaded")
		}
	}
}
