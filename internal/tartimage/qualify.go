package tartimage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestkeychain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/legacyios"
	"github.com/mallexxx/virfield/internal/qualify"
)

type session interface {
	qualify.Guest
	Close()
}

// guests is the image credential lifecycle; sshGuests is the real one.
type guests interface {
	Connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (session, error)
	Secure(ctx context.Context, l domain.Lease, ip string, g session) error
	Password(l domain.Lease) (string, error)
}

type sshGuests struct{ m *guestssh.Manager }

func (s sshGuests) Connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (session, error) {
	return s.m.ConnectReady(ctx, l, ip, bootstrap)
}

func (s sshGuests) Secure(ctx context.Context, l domain.Lease, ip string, g session) error {
	client, ok := g.(*guestssh.Client)
	if !ok {
		return domain.Err("guest_identity_failed", "Unexpected guest session")
	}
	return s.m.SecureImage(ctx, l, ip, client)
}

func (s sshGuests) Password(l domain.Lease) (string, error) {
	c, err := guestssh.LoadCredentials(s.m.CredentialPath(l))
	return c.Password, err
}

var qualifyDeviceSupport = qualify.DeviceSupport

// qualify secures the image credential, reboots, and verifies the golden
// with the pinned host key before writing evidence and shutting down cleanly.
func (b *Builder) qualify(ctx context.Context, l domain.Lease, p domain.ImageProfile, progress func(string) error) (err error) {
	// A failed stage must not keep a slot or a password-SSH guest running.
	defer func() {
		if err != nil {
			b.halt(l)
		}
	}()
	recipeDigest, err := os.ReadFile(filepath.Join(b.folder(l), "recipe.sha256"))
	if err != nil {
		return domain.Err("invalid_profile", "Image has no recorded build recipe")
	}
	ip, err := b.boot(ctx, l)
	if err != nil {
		return err
	}
	bootstrap, err := b.guests.Connect(ctx, l, ip, true)
	if err != nil {
		return err
	}
	if err := qualify.Desktop(ctx, bootstrap); err != nil {
		bootstrap.Close()
		return err
	}
	if err := legacyios.Install(ctx, bootstrap, p, func(out string) error {
		return os.WriteFile(filepath.Join(b.folder(l), "legacy-ios-install.log"), []byte(out), 0600)
	}); err != nil {
		bootstrap.Close()
		return err
	}
	if !l.Portable {
		if err := b.guests.Secure(ctx, l, ip, bootstrap); err != nil {
			bootstrap.Close()
			return err
		}
	} else {
		if _, err := guestkeychain.Ensure(ctx, bootstrap, l.GuestUser, l.ID, "admin"); err != nil {
			bootstrap.Close()
			return err
		}
	}
	if err := b.shutdown(ctx, l, bootstrap); err != nil {
		return err
	}
	if err := progress("Verifying the image after reboot"); err != nil {
		return err
	}
	ip, err = b.boot(ctx, l)
	if err != nil {
		return err
	}
	g, err := b.guests.Connect(ctx, l, ip, l.Portable)
	if err != nil {
		return err
	}
	evidence, err := b.verify(ctx, l, p, g, ip, progress)
	if err != nil {
		g.Close()
		return err
	}
	evidence["upstream"] = p.Tart.Upstream
	evidence["recipe_sha256"] = string(recipeDigest)
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	if err := os.WriteFile(filepath.Join(b.folder(l), "verification.json"), encoded, 0600); err != nil {
		g.Close()
		return err
	}
	return b.shutdown(ctx, l, g)
}

func (b *Builder) verify(ctx context.Context, l domain.Lease, p domain.ImageProfile, g session, ip string, progress func(string) error) (map[string]any, error) {
	user := l.GuestUser
	version, err := qualify.OS(ctx, g, p.MacOS, p.Build)
	if err != nil {
		return nil, err
	}
	sip, err := qualify.SIP(ctx, g, p.DisableSIP)
	if err != nil {
		return nil, err
	}
	if err := qualify.Desktop(ctx, g); err != nil {
		return nil, err
	}
	disk, err := qualify.DiskPolicy(ctx, g, true)
	if err != nil {
		return nil, err
	}
	password := "admin"
	if !l.Portable {
		password, err = b.guests.Password(l)
		if err != nil {
			return nil, err
		}
	}
	if err := qualify.Password(ctx, g, user, password); err != nil {
		return nil, err
	}
	keychain, err := guestkeychain.Verify(ctx, g, user, l.ID)
	if err != nil {
		return nil, err
	}
	logXcode := func(out string) error {
		return os.WriteFile(filepath.Join(b.folder(l), "xcode-verify.log"), []byte(out), 0600)
	}
	xcode := p.Xcode
	if xcode == nil && p.Provision == "uitest-27-v1" {
		// UI-test images adopt the upstream Xcode; record what is installed.
		installed, err := qualify.InstalledXcode(ctx, g)
		if err != nil {
			return nil, err
		}
		xcode = &installed
	}
	if xcode != nil {
		if err := qualify.Xcode(ctx, g, user, *xcode, logXcode); err != nil {
			return nil, err
		}
	}
	toolchain, err := legacyios.Verify(ctx, g, p, func(out string) error {
		return os.WriteFile(filepath.Join(b.folder(l), "legacy-ios-verify.log"), []byte(out), 0600)
	})
	if err != nil {
		return nil, err
	}
	if p.Security == "automation" {
		if err := qualify.Security(ctx, g, user, func(out string) error {
			return os.WriteFile(filepath.Join(b.folder(l), "security-verify.log"), []byte(out), 0600)
		}); err != nil {
			return nil, err
		}
	}
	if p.Provision == "uitest-27-v1" {
		logProvision := func(out string) error {
			return os.WriteFile(filepath.Join(b.folder(l), "provision-verify.log"), []byte(out), 0600)
		}
		if err := qualify.Provision(ctx, g, user, logProvision); err != nil {
			return nil, err
		}
	}
	support := ""
	if xcode != nil {
		support, err = b.deviceSupport(ctx, b.config.StateDir, ip, version, p.Build, progress)
		if err != nil {
			return nil, err
		}
	}
	evidence := map[string]any{"macos": version, "build": p.Build, "xcode": xcode, "sip": sip, "desktop": true, "disk": disk, "gui_keychain": keychain, "scoped_image_ssh": !l.Portable, "portable": l.Portable, "guest_user": user, "device_support": support, "provision": p.Provision, "security": p.Security, "verified_at": time.Now().UTC()}
	if toolchain != nil {
		evidence["toolchain"] = toolchain
	}
	return evidence, nil
}

func (b *Builder) boot(ctx context.Context, l domain.Lease) (string, error) {
	// Right after a Packer build or shutdown, tart can still hold the VM and
	// refuses to run it; retry briefly before giving up.
	for attempt := 1; ; attempt++ {
		err := b.config.Runtime.Start(ctx, l)
		if err == nil {
			break
		}
		if attempt == 10 {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(b.startRetry):
		}
	}
	if err := b.waitState(ctx, l, "running"); err != nil {
		return "", err
	}
	vm, _, err := b.vm(ctx, l)
	return vm.IP, err
}

// shutdown asks the guest for a clean halt; the session drops as it stops.
// A guest that ignores the request is stopped through Tart instead.
func (b *Builder) shutdown(ctx context.Context, l domain.Lease, g session) error {
	_, _ = g.Run(ctx, "sudo -n /sbin/shutdown -h now", "")
	g.Close()
	if err := b.waitState(ctx, l, "stopped"); err == nil {
		return nil
	}
	if err := b.config.Runtime.Stop(ctx, l); err != nil {
		return err
	}
	return b.waitState(ctx, l, "stopped")
}

// halt is best effort after a failure; the journal already records the error.
func (b *Builder) halt(l domain.Lease) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if vm, ok, err := b.vm(ctx, l); err == nil && ok && vm.State != "stopped" {
		_ = b.config.Runtime.Stop(ctx, l)
	}
}
