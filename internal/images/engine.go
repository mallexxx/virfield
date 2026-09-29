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
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 30 * time.Second, DisableCompression: true}
	client := &http.Client{Transport: tr, Timeout: 90 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return &Engine{Dir: dir, Backend: b, Tools: t, HTTP: client}, nil
}
func (e *Engine) Step(ctx context.Context, l domain.Lease, p domain.ImageProfile, step string, progress func(string) error) error {
	timeout, known := map[string]time.Duration{"download": 90 * time.Minute, "create": 45 * time.Minute, "setup": 15 * time.Minute, "assistant": 10 * time.Minute, "sip": 20 * time.Minute, "verify": 10 * time.Minute, "stop": 2 * time.Minute}[step]
	if !known {
		return domain.Err("invalid_profile", "unsupported image pipeline stage")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := p.Validate(); err != nil {
		return err
	}
	if p.Build != "26A428" {
		return domain.Err("unsupported_image", "Only the tested macOS 27 build 26A428 image recipe is supported")
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
		for _, path := range []string{e.Tools.Lume, e.Tools.Python, e.Tools.Tesseract, filepath.Join(e.Tools.VNCBin, "vncdotool")} {
			st, err := os.Stat(path)
			if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
				return domain.Err("image_dependency", "An image tool is missing or not executable; check image_tools configuration")
			}
		}
		if err := lume.VerifyImageVersion(ctx, e.Tools.Lume); err != nil {
			return err
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
		if step == "create" {
			if err := Space(e.Dir, 48<<30); err != nil {
				return err
			}
		}
		return lume.ImageCommand(ctx, e.Tools.Lume, e.Tools.VNCBin, folder, step, l, ipsw)
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
		out, err := e.waitDesktopState(ctx, guest)
		if err != nil {
			return err
		}
		if out == "assistant" {
			endpoint, err := e.Backend.VNCAddress(ctx, l)
			if err != nil {
				return err
			}
			input, _ := json.Marshal(map[string]string{"url": endpoint, "directory": filepath.Join(folder, "assistant", time.Now().UTC().Format("20060102T150405.000000000Z")), "tesseract": e.Tools.Tesseract})
			cmd := exec.CommandContext(ctx, e.Tools.Python, "-c", assistantScript)
			cmd.Stdin = bytes.NewReader(input)
			if err := lume.RunPrivate(ctx, cmd, filepath.Join(folder, "assistant.log")); err != nil {
				return err
			}
		}
		if err := e.desktop(ctx, guest); err != nil {
			return err
		}
		return e.stop(ctx, l)
	case "sip":
		if !p.DisableSIP {
			return progress("SIP kept enabled by image profile")
		}
		return e.sip27(ctx, l)
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
		if err := e.desktop(ctx, guest); err != nil {
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
		if err := e.desktop(ctx, secured); err != nil {
			return err
		}
		evidence, _ := json.MarshalIndent(map[string]any{"build": p.Build, "ipsw_sha256": p.SHA256, "sip": strings.TrimSpace(sip), "desktop": true, "scoped_image_ssh": true, "verified_at": time.Now().UTC()}, "", "  ")
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
	for _, v := range o.VMs {
		if v.Key() == l.Key() && v.State == "stopped" {
			return nil
		}
	}
	if err := e.Backend.Stop(ctx, l); err != nil {
		return err
	}
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
			return domain.Err("stop_timeout", "Image did not reach stopped state")
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
	for {
		out, err := g.Run(wait, "if /usr/bin/pgrep -x 'Setup Assistant' >/dev/null; then echo assistant; elif test -f /var/db/.AppleSetupDone && /usr/bin/pgrep -x Finder >/dev/null; then echo desktop; else echo waiting; fi", "")
		if err != nil {
			return "", err
		}
		state := strings.TrimSpace(out)
		if state == "assistant" || state == "desktop" {
			return state, nil
		}
		if err := pause(wait, 2*time.Second); err != nil {
			return "", domain.Err("desktop_timeout", "Guest login did not reach Setup Assistant or Finder within two minutes")
		}
	}
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
