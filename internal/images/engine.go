package images

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/lume"
)

//go:embed assistant.py
var assistantScript string

type Engine struct {
	Dir     string
	Backend *lume.Client
	Tools   domain.ImageTools
	HTTP    *http.Client
}

func New(dir string, b *lume.Client, t domain.ImageTools) (*Engine, error) {
	for _, p := range []string{dir, t.Lume, t.Python, t.Tesseract, t.VNCBin} {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("image tool paths must be absolute")
		}
	}
	for _, p := range []string{t.Xcode, t.XcodeArchives, t.AppleCookies} {
		if p != "" && !filepath.IsAbs(p) {
			return nil, fmt.Errorf("xcode bundle/archive and Apple cookie paths must be absolute")
		}
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 30 * time.Second, DisableCompression: true}
	client := &http.Client{Transport: tr, Timeout: 90 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return &Engine{Dir: dir, Backend: b, Tools: t, HTTP: client}, nil
}
func (e *Engine) Step(ctx context.Context, l domain.Lease, p domain.ImageProfile, step string, progress func(string) error) error {
	timeout, known := map[string]time.Duration{"download": 3 * time.Hour, "create": 45 * time.Minute, "setup": 25 * time.Minute, "assistant": 10 * time.Minute, "sip": 20 * time.Minute, "provision": 2 * time.Hour, "verify": 10 * time.Minute, "stop": 2 * time.Minute}[step]
	if !known {
		return domain.Err("invalid_profile", "unsupported image pipeline stage")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := p.Validate(); err != nil {
		return err
	}
	if !domain.ValidName(l.ID) || !domain.ValidName(l.VMName) || !domain.ValidName(l.Location) {
		return domain.Err("invalid_profile", "invalid image identity")
	}
	folder := filepath.Join(e.Dir, "images", l.ID)
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	ipsw := filepath.Join(e.Dir, "cache", p.SHA256+".ipsw")
	switch step {
	case "download":
		if p.Provision == "uitest-27-v1" {
			if _, err := e.checkXcode(ctx); err != nil {
				return err
			}
		}
		for _, path := range []string{e.Tools.Lume, e.Tools.Python, e.Tools.Tesseract, filepath.Join(e.Tools.VNCBin, "vncdotool")} {
			st, err := os.Stat(path)
			if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
				return domain.Err("image_dependency", "An image tool is missing or not executable; check image_tools configuration")
			}
		}
		if err := lume.VerifyImageVersion(ctx, e.Tools.Lume); err != nil {
			return err
		}
		if p.Xcode != nil {
			if _, err := e.xcodeSource(ctx, *p.Xcode, progress); err != nil {
				return err
			}
		}
		// A full restore plus a new guest and safety headroom must fit together.
		if err := Space(e.Dir, p.Size+(48<<30)); err != nil {
			return err
		}
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			_, err = Download(ctx, e.HTTP, filepath.Join(e.Dir, "cache"), p, progress)
			if err == nil {
				return nil
			}
			failure, ok := err.(*domain.Error)
			if !ok || failure.Code != "download_failed" || ctx.Err() != nil {
				return err
			}
			if attempt < 2 {
				if err := progress("Download interrupted; resuming retained bytes"); err != nil {
					return err
				}
				if err := pause(ctx, time.Duration(attempt+1)*2*time.Second); err != nil {
					return err
				}
			}
		}
		return err
	case "create", "setup":
		if step == "setup" && onlineSetupRequired(p) {
			return e.setupOnline(ctx, l, progress)
		}
		if step == "create" {
			if err := Space(e.Dir, 48<<30); err != nil {
				return err
			}
		}
		display := "1920x1080"
		if onlineSetupRequired(p) {
			// Native Monterey Assistant needs more than 540 logical pixels of
			// height on Lume's Retina display, otherwise its buttons are clipped.
			display = "1920x1440"
		}
		return lume.ImageCommand(ctx, e.Tools.Lume, e.Tools.VNCBin, folder, step, l, ipsw, display)
	case "assistant":
		vm, err := e.boot(ctx, l)
		if err != nil {
			return err
		}
		guest, err := e.connect(ctx, l, vm.IP, true)
		if err != nil {
			return err
		}
		defer guest.Close()
		if err := e.prepareDesktop(ctx, l, guest); err != nil {
			return err
		}
		// Establish native image credentials before Recovery creates volume-owner
		// policy. Recovery must authenticate with this identity, not lume/lume.
		if err := e.secure(ctx, l, vm.IP, guest); err != nil {
			return err
		}
		return e.stop(ctx, l)
	case "sip":
		if !p.DisableSIP {
			return progress("SIP kept enabled by image profile")
		}
		return e.sip27(ctx, l)
	case "provision":
		return e.provision(ctx, l, p, progress)
	case "verify":
		vm, err := e.boot(ctx, l)
		if err != nil {
			return err
		}
		guest, err := e.connect(ctx, l, vm.IP, true)
		if err != nil {
			return err
		}
		defer guest.Close()
		build, err := guest.Run(ctx, "/usr/bin/sw_vers -buildVersion", "")
		if err != nil {
			return err
		}
		if strings.TrimSpace(build) != p.Build {
			return domain.Err("guest_build_mismatch", "Guest macOS build differs from the image manifest")
		}
		if p.MacOS != "" {
			version, err := guest.Run(ctx, "/usr/bin/sw_vers -productVersion", "")
			if err != nil {
				return err
			}
			actual := strings.TrimSpace(version)
			if !domain.ValidVersion(actual) || domain.CompareVersions(actual, p.MacOS) != 0 {
				return domain.Err("guest_version_mismatch", "Guest macOS version differs from the selected catalog release")
			}
		}
		sip, err := guest.Run(ctx, "/usr/bin/csrutil status", "")
		if err != nil {
			return err
		}
		expected := "System Integrity Protection status: enabled."
		if p.DisableSIP {
			expected = "System Integrity Protection status: disabled."
		}
		if strings.TrimSpace(sip) != expected {
			return domain.Err("sip_verification_failed", "Guest SIP status does not match the requested canonical state")
		}
		if err := e.prepareDesktop(ctx, l, guest); err != nil {
			return err
		}
		if err := e.secure(ctx, l, vm.IP, guest); err != nil {
			return err
		}
		// Reboot after credential rotation to verify autologin and a usable
		// desktop on the next boot, not just in the bootstrap session.
		guest.Close()
		if err := e.stop(ctx, l); err != nil {
			return err
		}
		rebooted, err := e.boot(ctx, l)
		if err != nil {
			return err
		}
		secured, err := e.connect(ctx, l, rebooted.IP, false)
		if err != nil {
			return err
		}
		defer secured.Close()
		if err := e.prepareDesktop(ctx, l, secured); err != nil {
			return err
		}
		credentials, err := guestssh.LoadCredentials((&guestssh.Manager{Dir: e.Dir}).CredentialPath(l))
		if err != nil {
			return err
		}
		if _, err := secured.Run(ctx, "/bin/bash -c 'IFS= read -r password; /usr/bin/dscl . -authonly lume \"$password\"'", credentials.Password+"\n"); err != nil {
			return domain.Err("credential_verification_failed", "Image password did not persist across reboot; image will not be published")
		}
		if p.Xcode != nil {
			if err := e.verifyXcode(ctx, l, secured, *p.Xcode); err != nil {
				return err
			}
		}
		if p.Provision == "uitest-27-v1" {
			if err := e.verifyProvision(ctx, l, secured); err != nil {
				return err
			}
		}
		evidence, _ := json.MarshalIndent(map[string]any{"macos": p.MacOS, "xcode": p.Xcode, "build": p.Build, "ipsw_sha256": p.SHA256, "sip": strings.TrimSpace(sip), "desktop": true, "scoped_image_ssh": true, "provision": p.Provision, "verified_at": time.Now().UTC()}, "", "  ")
		return os.WriteFile(filepath.Join(folder, "verification.json"), evidence, 0600)
	case "stop":
		return e.stop(ctx, l)
	}
	return domain.Err("invalid_profile", "unsupported image pipeline stage")
}
func (e *Engine) boot(ctx context.Context, l domain.Lease) (domain.VM, error) {
	o, err := e.Backend.Observe(ctx)
	if err != nil {
		return domain.VM{}, err
	}
	found := false
	for _, v := range o.VMs {
		if v.Key() == l.Key() {
			found = true
			if v.State == "stopped" {
				if err := e.Backend.Start(ctx, l); err != nil {
					return domain.VM{}, err
				}
			} else if v.State != "running" {
				return domain.VM{}, domain.Err("unexpected_state", "Image VM cannot be booted from current state")
			}
		}
	}
	if !found {
		return domain.VM{}, domain.Err("vm_missing", "Image VM is absent")
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		o, err := e.Backend.Observe(wait)
		if err == nil {
			for _, v := range o.VMs {
				if v.Key() == l.Key() && v.State == "running" && net.ParseIP(v.IP) != nil && v.SSHAvailable {
					return v, nil
				}
			}
		}
		if err := pause(wait, 2*time.Second); err != nil {
			return domain.VM{}, domain.Err("guest_timeout", "Image did not reach SSH readiness within five minutes")
		}
	}
}
func (e *Engine) stop(ctx context.Context, l domain.Lease) error {
	o, err := e.Backend.Observe(ctx)
	if err != nil {
		return err
	}
	var vm domain.VM
	for _, v := range o.VMs {
		if v.Key() == l.Key() {
			vm = v
		}
	}
	if vm.State == "stopped" {
		return nil
	}
	if vm.State != "running" {
		return domain.Err("unexpected_state", "Image must be running or already stopped for clean shutdown")
	}
	credentials, err := guestssh.LoadCredentials((&guestssh.Manager{Dir: e.Dir}).CredentialPath(l))
	if err != nil {
		return err
	}
	guest, err := e.connect(ctx, l, vm.IP, !credentials.Secured)
	if err != nil {
		return err
	}
	password := credentials.Password
	if !credentials.Secured {
		password = "lume"
	}
	// Lume's forced stop can discard pending guest directory-service writes.
	// Request one normal guest shutdown and observe completion; never turn an
	// ambiguous SSH disconnect into a repeated command or forced power-off.
	_, shutdownErr := guest.Run(ctx, "/bin/bash -c 'IFS= read -r password; printf \"%s\\n\" \"$password\" | sudo -S -p \"\" /sbin/shutdown -h now'", password+"\n")
	guest.Close()
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		o, err := e.Backend.Observe(wait)
		if err == nil {
			for _, v := range o.VMs {
				if v.Key() == l.Key() && v.State == "stopped" {
					return nil
				}
			}
		}
		if err := pause(wait, time.Second); err != nil {
			if shutdownErr != nil {
				return domain.Err("guest_shutdown_failed", "Guest shutdown was not confirmed; inspect the image before retry, no forced stop was attempted")
			}
			return domain.Err("stop_timeout", "Image did not complete clean shutdown within two minutes; no forced stop was attempted")
		}
	}
}
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (e *Engine) waitDesktopState(ctx context.Context, g *guest) (string, error) {
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return waitDesktop(wait, 2*time.Second, 5*time.Second, func(ctx context.Context) (string, error) {
		return g.Run(ctx, "if /usr/bin/pgrep -x 'Setup Assistant' >/dev/null; then echo assistant; elif test -f /var/db/.AppleSetupDone && /usr/bin/pgrep -x Finder >/dev/null; then echo desktop; else echo waiting; fi", "")
	})
}

func waitDesktop(ctx context.Context, poll, settle time.Duration, observe func(context.Context) (string, error)) (string, error) {
	var desktopSince time.Time
	for {
		out, err := observe(ctx)
		if err != nil {
			return "", err
		}
		state := strings.TrimSpace(out)
		if state == "assistant" {
			return state, nil
		}
		// Finder can appear before the login-time Assistant. Require a settled
		// desktop before rotating credentials or declaring the GUI ready.
		if state == "desktop" {
			if desktopSince.IsZero() {
				desktopSince = time.Now()
			} else if time.Since(desktopSince) >= settle {
				return state, nil
			}
		} else {
			desktopSince = time.Time{}
		}
		if err := pause(ctx, poll); err != nil {
			return "", domain.Err("desktop_timeout", "Guest login did not reach Setup Assistant or Finder within two minutes")
		}
	}
}

// prepareDesktop handles the login-time Assistant that can return after Recovery,
// as well as the initial setup. It is part of the fixed image job, never a manual
// guest repair or a readiness shortcut. Unknown screens stop the stage.
func (e *Engine) prepareDesktop(ctx context.Context, l domain.Lease, g *guest) error {
	state, err := e.waitDesktopState(ctx, g)
	if err != nil {
		return err
	}
	if state == "desktop" {
		return nil
	}
	if state == "assistant" {
		endpoint, err := e.Backend.VNCAddress(ctx, l)
		if err != nil {
			return err
		}
		folder := filepath.Join(e.Dir, "images", l.ID)
		attempt := filepath.Join(folder, "assistant", time.Now().UTC().Format("20060102T150405.000000000Z"))
		if err := os.MkdirAll(attempt, 0700); err != nil {
			return err
		}
		input, _ := json.Marshal(map[string]string{"url": endpoint, "directory": attempt, "tesseract": e.Tools.Tesseract})
		bounded, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(bounded, e.Tools.Python, "-c", assistantScript)
		cmd.Stdin = bytes.NewReader(input)
		if err := lume.RunPrivate(bounded, cmd, filepath.Join(attempt, "assistant.log")); err != nil {
			return err
		}
	}
	return e.desktop(ctx, g)
}

func (e *Engine) desktop(ctx context.Context, g *guest) error {
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		state, err := e.waitDesktopState(wait, g)
		if err != nil {
			return err
		}
		if state == "desktop" {
			return nil
		}
		if err := pause(wait, 2*time.Second); err != nil {
			return domain.Err("assistant_incomplete", "Guest Setup Assistant has not exited to a verified Finder desktop")
		}
	}
}
