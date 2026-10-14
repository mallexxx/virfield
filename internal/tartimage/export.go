package tartimage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/qualify"
	"github.com/mallexxx/virfield/internal/registry"
)

type ImageRegistry interface {
	Source(string) (domain.RegistrySource, error)
	Resolve(context.Context, domain.RegistryResolveRequest) (domain.RegistryReference, error)
}

// ExportStep operates exclusively on a fresh portable rebuild. The source
// golden and its private identity are never cloned or sent to the registry.
func (b *Builder) ExportStep(ctx context.Context, l domain.Lease, target domain.RegistryExport, step string) (string, error) {
	if !l.Portable || l.Source == nil || l.Source.Image == nil || l.Source.Image.Tart == nil || l.VMName == l.Source.Name || !strings.HasPrefix(l.VMName, "vf-export-") || l.GuestUser != "admin" || b.config.Registry == nil {
		return "", domain.Err("registry_push_disabled", "Publication requires a fresh portable Tart rebuild")
	}
	if !domain.ValidName(l.VMName) || !domain.ValidName(l.ID) {
		return "", domain.Err("invalid_profile", "Invalid portable VM identity")
	}
	home, ok := b.config.Homes[l.Location]
	if !ok {
		return "", domain.Err("invalid_profile", "Unknown Tart container")
	}
	marker := filepath.Join(b.folder(l), "sanitized-vm")
	switch step {
	case "sanitize":
		ip, err := b.boot(ctx, l)
		if err != nil {
			b.halt(l)
			return "", err
		}
		g, err := b.guests.Connect(ctx, l, ip, true)
		if err != nil {
			b.halt(l)
			return "", err
		}
		// This guest still uses the PUBLIC bootstrap account. No SecureImage or
		// scoped management key has been installed in this rebuilt artifact.
		script := `set -euo pipefail
[[ "$(sysctl -n hw.model)" == VirtualMac* ]]
/usr/bin/dscl . -authonly admin admin
/bin/rm -rf /Users/admin/.ssh /var/root/.ssh /Users/admin/.virfield-xcode
/bin/rm -f /Users/admin/.zsh_history /Users/admin/.bash_history /Users/admin/.git-credentials /Users/admin/.netrc
/usr/bin/find /Users/admin/Library/Logs /Library/Logs -type f -delete
[[ ! -e /Users/admin/.ssh && ! -e /var/root/.ssh ]]
`
		_, err = g.Run(ctx, "sudo -n /bin/bash -c "+qualify.Quote(script), "")
		if err != nil {
			g.Close()
			b.halt(l)
			return "", domain.Err("registry_sanitize_failed", "Portable guest sanitization failed")
		}
		if err = b.shutdown(ctx, l, g); err != nil {
			return "", err
		}
		return "", os.WriteFile(marker, []byte(l.Key()), 0600)
	case "upload":
		raw, err := os.ReadFile(marker)
		if err != nil || string(raw) != l.Key() {
			return "", domain.Err("registry_sanitize_failed", "Portable VM has no sanitization evidence")
		}
		vm, exists, err := b.vm(ctx, l)
		if err != nil || !exists || vm.State != "stopped" {
			return "", domain.Err("registry_publish_unknown", "Portable VM must be observed stopped before upload")
		}
		source, err := b.config.Registry.Source(target.Source)
		if err != nil {
			return "", err
		}
		if !source.AllowPush || source.Organization != target.Organization {
			return "", domain.Err("registry_push_disabled", "Publication target is not operator-approved")
		}
		token, err := registry.Credentials(source)
		if err != nil {
			return "", err
		}
		request := domain.RegistryResolveRequest{Source: target.Source, Repository: target.Repository, Tag: target.Tag}
		if err = request.Validate(); err != nil {
			return "", err
		}
		// Recheck immediately before the single non-retried push.
		_, err = b.config.Registry.Resolve(ctx, request)
		if err == nil {
			return "", domain.Err("registry_tag_exists", "Publication requires an absent tag")
		}
		var failure *domain.Error
		if !errors.As(err, &failure) || failure.Code != "registry_not_found" {
			return "", err
		}
		env := []string{"TART_HOME=" + home, "TART_REGISTRY_HOSTNAME=ghcr.io", "TART_REGISTRY_USERNAME=" + source.Username, "TART_REGISTRY_PASSWORD=" + token}
		ref := "ghcr.io/" + target.Organization + "/" + target.Repository + ":" + target.Tag
		if err = b.run.Run(ctx, b.folder(l), env, filepath.Join(b.folder(l), "registry-push.log"), b.config.TartBinary, "push", l.VMName, ref); err != nil {
			return "", domain.Err("registry_publish_unknown", "Tart push outcome is unknown; inspect the target before recovery")
		}
		resolved, err := b.config.Registry.Resolve(ctx, request)
		if err != nil {
			return "", domain.Err("registry_publish_unknown", "Uploaded Tart manifest could not be verified")
		}
		return resolved.Digest, nil
	}
	return "", domain.Err("invalid_profile", "Unsupported publication stage")
}
