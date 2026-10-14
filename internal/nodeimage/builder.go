// Package nodeimage secures a node's bootstrap golden into the golden that
// node VM leases clone: clone the bootstrap disk, rotate its credentials with
// the guest SSH pipeline, verify it after a reboot, and commit it read-only.
package nodeimage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/qualify"
)

// Runtime runs image VMs on the node that holds the goldens.
type Runtime interface {
	Observe(context.Context) (domain.Observation, error)
	CloneImage(ctx context.Context, l domain.Lease, golden string) error
	StartImage(context.Context, domain.Lease) error
	StopImage(context.Context, domain.Lease) error
	CommitImage(context.Context, domain.Lease) error
}

type Session interface {
	qualify.Guest
	Close()
}

// Guests is the image credential lifecycle; SSHGuests is the real one.
type Guests interface {
	Connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (Session, error)
	Secure(ctx context.Context, l domain.Lease, ip string, g Session) error
	Password(l domain.Lease) (string, error)
}

type Config struct {
	StateDir string
	Runtime  Runtime
	Guests   Guests
}

type Builder struct {
	config Config
	// poll and bootTimeout bound waiting for the image VM; shutdownGrace lets
	// a guest shutdown flush before the VM is stopped, since macOS clones can
	// hang at the very end of a shutdown.
	poll, bootTimeout, shutdownGrace time.Duration
	desktop                          func(context.Context, Session) error
}

func New(config Config) *Builder {
	return &Builder{config: config, poll: 3 * time.Second, bootTimeout: 10 * time.Minute, shutdownGrace: 45 * time.Second, desktop: func(ctx context.Context, g Session) error { return qualify.Desktop(ctx, g) }}
}

func (b *Builder) folder(l domain.Lease) string {
	return filepath.Join(b.config.StateDir, "images", l.ID)
}

// Step runs one controller phase of a node image build.
func (b *Builder) Step(ctx context.Context, l domain.Lease, p domain.ImageProfile, step string, progress func(string) error) error {
	if p.Node == nil || l.GuestUser != "admin" || !domain.ValidName(l.VMName) || strings.HasPrefix(l.VMName, "vf-") {
		return domain.Err("invalid_profile", "Node image builds need a node source, the admin account and an image name")
	}
	switch step {
	case "build":
		return b.config.Runtime.CloneImage(ctx, l, p.Node.Golden)
	case "qualify":
		return b.qualify(ctx, l, p, progress)
	case "stop":
		if err := b.config.Runtime.StopImage(ctx, l); err != nil {
			return err
		}
		_, err := b.wait(ctx, l, "stopped")
		return err
	case "commit":
		return b.config.Runtime.CommitImage(ctx, l)
	}
	return domain.Err("invalid_profile", "Unknown node image step "+step)
}

// qualify secures the clone's credentials, reboots, verifies the golden over
// the pinned host key and writes evidence before shutting it down.
func (b *Builder) qualify(ctx context.Context, l domain.Lease, p domain.ImageProfile, progress func(string) error) (err error) {
	// A failed stage must not keep a password-SSH guest running.
	defer func() {
		if err != nil {
			_ = b.config.Runtime.StopImage(context.WithoutCancel(ctx), l)
		}
	}()
	if err := os.MkdirAll(b.folder(l), 0700); err != nil {
		return err
	}
	l, err = b.boot(ctx, l)
	if err != nil {
		return err
	}
	bootstrap, err := b.config.Guests.Connect(ctx, l, l.IP, true)
	if err != nil {
		return err
	}
	if err := b.config.Guests.Secure(ctx, l, l.IP, bootstrap); err != nil {
		bootstrap.Close()
		return err
	}
	if err := b.shutdown(ctx, l, bootstrap); err != nil {
		return err
	}
	if err := progress("Credentials rotated; verifying the image after reboot"); err != nil {
		return err
	}
	l, err = b.boot(ctx, l)
	if err != nil {
		return err
	}
	g, err := b.config.Guests.Connect(ctx, l, l.IP, false)
	if err != nil {
		return err
	}
	evidence, err := b.verify(ctx, l, p, g)
	if err != nil {
		g.Close()
		return err
	}
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	if err := os.WriteFile(filepath.Join(b.folder(l), "verification.json"), encoded, 0600); err != nil {
		g.Close()
		return err
	}
	return b.shutdown(ctx, l, g)
}

func (b *Builder) verify(ctx context.Context, l domain.Lease, p domain.ImageProfile, g Session) (map[string]any, error) {
	version, err := qualify.OS(ctx, g, p.MacOS, p.Build)
	if err != nil {
		return nil, err
	}
	sip, err := qualify.SIP(ctx, g, p.DisableSIP)
	if err != nil {
		return nil, err
	}
	if err := b.desktop(ctx, g); err != nil {
		return nil, err
	}
	password, err := b.config.Guests.Password(l)
	if err != nil {
		return nil, err
	}
	if err := qualify.Password(ctx, g, l.GuestUser, password); err != nil {
		return nil, err
	}
	return map[string]any{"golden": p.Node.Golden, "macos": version, "build": p.Build, "sip": sip}, nil
}

// boot starts the image VM and returns the lease with its guest address.
func (b *Builder) boot(ctx context.Context, l domain.Lease) (domain.Lease, error) {
	if err := b.config.Runtime.StartImage(ctx, l); err != nil {
		return l, err
	}
	vm, err := b.wait(ctx, l, "running")
	if err != nil {
		return l, err
	}
	l.IP, l.SSHPort = vm.IP, vm.SSHPort
	return l, nil
}

// shutdown asks the guest to flush and power off, then stops the VM.
func (b *Builder) shutdown(ctx context.Context, l domain.Lease, g Session) error {
	_, _ = g.Run(ctx, "/bin/sync; sudo -n /sbin/shutdown -h now", "")
	g.Close()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(b.shutdownGrace):
	}
	if err := b.config.Runtime.StopImage(ctx, l); err != nil {
		return err
	}
	_, err := b.wait(ctx, l, "stopped")
	return err
}

// wait polls the node until the image VM reaches state; a running VM must
// also answer guest SSH, which takes minutes for a clone booting off the HDD.
func (b *Builder) wait(ctx context.Context, l domain.Lease, state string) (domain.VM, error) {
	deadline := time.Now().Add(b.bootTimeout)
	for {
		o, err := b.config.Runtime.Observe(ctx)
		if err == nil {
			for _, vm := range o.VMs {
				if vm.Key() == l.Key() && vm.State == state && (state != "running" || (vm.IP != "" && vm.SSHPort > 0 && vm.SSHAvailable)) {
					return vm, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return domain.VM{}, domain.Err("image_timeout", "Image VM did not become "+state)
		}
		select {
		case <-ctx.Done():
			return domain.VM{}, ctx.Err()
		case <-time.After(b.poll):
		}
	}
}

// SSHGuests adapts a guestssh manager whose dialer reaches node VMs.
func SSHGuests(m *guestssh.Manager) Guests { return sshGuests{m} }

type sshGuests struct{ m *guestssh.Manager }

func (s sshGuests) Connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (Session, error) {
	return s.m.ConnectReady(ctx, l, ip, bootstrap)
}

func (s sshGuests) Secure(ctx context.Context, l domain.Lease, ip string, g Session) error {
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

func (b *Builder) ValidateTemplate(t domain.Template) error {
	if t.Image == nil || t.Image.Node == nil || t.GuestUser != "admin" || strings.HasPrefix(t.Name, "vf-") {
		return domain.Err("invalid_profile", "Node builds require a named bootstrap golden and the admin account")
	}
	return t.Image.Validate()
}
