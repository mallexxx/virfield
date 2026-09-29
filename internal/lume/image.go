package lume

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// ImageCommand is the explicit CLI portion of the same Lume adapter. Lume 0.5.3
// exposes offline setup here, not in its HTTP lifecycle API.
// Only these fixed operations can be selected; clients never supply argv.
func ImageCommand(ctx context.Context, binary, toolsDir, logDir, step string, l domain.Lease, ipsw string) error {
	if !domain.ValidName(l.VMName) || !domain.ValidName(l.Location) || !filepath.IsAbs(binary) {
		return domain.Err("invalid_profile", "invalid image command configuration")
	}
	var args []string
	switch step {
	case "create":
		args = []string{"create", l.VMName, "--os", "macos", "--ipsw", ipsw, "--cpu", "4", "--memory", "8GB", "--disk-size", "80GB", "--display", "1920x1080", "--storage", l.Location}
	case "setup":
		args = []string{"setup", l.VMName, "--unattended", "tahoe", "--storage", l.Location, "--vnc-port", "0"}
	default:
		return domain.Err("invalid_profile", "unsupported image stage")
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "PATH="+toolsDir+":/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin")
	return RunPrivate(ctx, cmd, filepath.Join(logDir, step+".log"))
}

// RunPrivate bounds output and cancellation for owned image subprocesses. Logs
// may include bootstrap details and therefore never enter API events or stdout.
func RunPrivate(ctx context.Context, cmd *exec.Cmd, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := &limitedLog{w: f, left: 8 << 20}
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return domain.Err("image_interrupted", "Image stage canceled or timed out; inspect VM and private logs before recovery")
		}
		return domain.Err("image_command_failed", "Image subprocess failed; inspect the private stage log before recovery")
	}
	return f.Sync()
}

type limitedLog struct {
	mu   sync.Mutex
	w    io.Writer
	left int
}

func (w *limitedLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if w.left > 0 {
		take := min(n, w.left)
		if _, err := w.w.Write(p[:take]); err != nil {
			return 0, err
		}
		w.left -= take
	}
	return n, nil
}

// VNCAddress returns a private endpoint for the exact image currently being
// prepared. It is never included in public inventory or persisted events.
func (c *Client) VNCAddress(ctx context.Context, l domain.Lease) (string, error) {
	var v struct {
		URL string `json:"vncUrl"`
	}
	if err := c.request(ctx, "GET", "/lume/vms/"+l.VMName+"?storage="+l.Location, nil, &v); err != nil {
		return "", err
	}
	return v.URL, nil
}

func VerifyImageVersion(ctx context.Context, binary string) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(checkCtx, binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "0.5.3" {
		return domain.Err("unsupported_lume", "Image pipeline requires tested Lume 0.5.3")
	}
	return nil
}
