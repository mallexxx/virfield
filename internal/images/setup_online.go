package images

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/lume"
)

//go:embed setup_online.py
var onlineSetupScript string

// Monterey's Data volume can only be unlocked by its running guest. Do not try
// to modify that encrypted volume from the host: complete the native Assistant.
func onlineSetupRequired(p domain.ImageProfile) bool {
	return domain.ValidVersion(p.MacOS) && domain.CompareVersions(p.MacOS, "13") < 0
}
func (e *Engine) setupOnline(ctx context.Context, l domain.Lease, progress func(string) error) error {
	if err := progress("Completing Setup Assistant inside the running guest"); err != nil {
		return err
	}
	observation, err := e.Backend.Observe(ctx)
	if err != nil {
		return err
	}
	found := false
	running := false
	for _, vm := range observation.VMs {
		if vm.Key() == l.Key() {
			found = true
			running = vm.State == "running"
			if vm.State != "stopped" && !running {
				return domain.Err("unexpected_state", "Online setup requires the exact image to be running or stopped")
			}
		}
	}
	if !found {
		return domain.Err("vm_missing", "Image is absent before online setup")
	}
	if !running {
		if err := e.Backend.Start(ctx, l); err != nil {
			return err
		}
	}
	ready, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	endpoint := ""
	for {
		current, observeErr := e.Backend.Observe(ready)
		isRunning := false
		if observeErr == nil {
			for _, v := range current.VMs {
				if v.Key() == l.Key() && v.State == "running" {
					isRunning = true
				}
			}
		}
		if isRunning {
			endpoint, err = e.Backend.VNCAddress(ready, l)
			if err == nil {
				u, parseErr := url.Parse(endpoint)
				if parseErr == nil && u.Scheme == "vnc" && u.Port() != "" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()) {
					conn, dialErr := (&net.Dialer{Timeout: time.Second}).DialContext(ready, "tcp", net.JoinHostPort(u.Hostname(), u.Port()))
					if dialErr == nil {
						conn.Close()
						break
					}
				}
			}
		}
		if err := pause(ready, time.Second); err != nil {
			return domain.Err("setup_unavailable", "Fresh guest VNC did not become available for Setup Assistant")
		}
	}

	folder := filepath.Join(e.Dir, "images", l.ID, "setup-online", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	input, _ := json.Marshal(map[string]string{"url": endpoint, "directory": folder, "tesseract": e.Tools.Tesseract})
	command := exec.CommandContext(ctx, e.Tools.Python, "-c", onlineSetupScript)
	command.Stdin = bytes.NewReader(input)
	if err := lume.RunPrivate(ctx, command, filepath.Join(folder, "setup.log")); err != nil {
		return err
	}
	vm, err := e.boot(ctx, l)
	if err != nil {
		return err
	}
	g, err := e.connect(ctx, l, vm.IP, true)
	if err != nil {
		return err
	}
	defer g.Close()
	if err := e.desktop(ctx, g); err != nil {
		return err
	}
	// Establish reboot-safe automatic login before stopping the native setup.
	if err := guestssh.BootstrapAutoLogin(ctx, g); err != nil {
		return err
	}
	return e.stop(ctx, l)
}
