package tartimage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
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

func guestUser(l domain.Lease) string {
	if l.GuestUser == "" {
		return "admin"
	}
	return l.GuestUser
}

// qualify secures the image credential, reboots, and verifies the golden
// with the pinned host key before writing evidence and shutting down cleanly.
func (b *Builder) qualify(ctx context.Context, l domain.Lease, p domain.ImageProfile, progress func(string) error) error {
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
	if err := b.guests.Secure(ctx, l, ip, bootstrap); err != nil {
		bootstrap.Close()
		return err
	}
	if err := b.shutdown(ctx, l, bootstrap); err != nil {
		return err
	}
	if err := progress("Credentials rotated; verifying the image after reboot"); err != nil {
		return err
	}
	ip, err = b.boot(ctx, l)
	if err != nil {
		return err
	}
	g, err := b.guests.Connect(ctx, l, ip, false)
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
	user := guestUser(l)
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
	password, err := b.guests.Password(l)
	if err != nil {
		return nil, err
	}
	if err := qualify.Password(ctx, g, user, password); err != nil {
		return nil, err
	}
	out, err := qualify.Xcode(ctx, g, user, *p.Xcode)
	if out != "" {
		_ = os.WriteFile(filepath.Join(b.folder(l), "xcode-verify.log"), []byte(out), 0600)
	}
	if err != nil {
		return nil, err
	}
	support, err := b.deviceSupport(ctx, b.config.StateDir, ip, version, p.Build, progress)
	if err != nil {
		return nil, err
	}
	return map[string]any{"macos": version, "build": p.Build, "xcode": p.Xcode, "sip": sip, "desktop": true, "disk": disk, "scoped_image_ssh": true, "guest_user": user, "device_support": support, "provision": p.Provision, "security": p.Security, "verified_at": time.Now().UTC()}, nil
}

func (b *Builder) boot(ctx context.Context, l domain.Lease) (string, error) {
	if err := b.config.Runtime.Start(ctx, l); err != nil {
		return "", err
	}
	if err := b.waitState(ctx, l, "running"); err != nil {
		return "", err
	}
	vm, _, err := b.vm(ctx, l)
	return vm.IP, err
}

// shutdown asks the guest for a clean halt; the session drops as it stops.
func (b *Builder) shutdown(ctx context.Context, l domain.Lease, g session) error {
	_, _ = g.Run(ctx, "sudo -n /sbin/shutdown -h now", "")
	g.Close()
	return b.waitState(ctx, l, "stopped")
}
