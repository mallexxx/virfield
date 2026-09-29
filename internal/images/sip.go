package images

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/lume"
)

//go:embed recovery27.py
var recovery27Script string

func (e *Engine) sip27(ctx context.Context, l domain.Lease) error {
	vm, err := e.boot(ctx, l)
	if err != nil {
		return err
	}
	guest, err := e.connect(ctx, l, vm.IP, true)
	if err != nil {
		return err
	}
	status, err := guest.run(ctx, "/usr/bin/csrutil status", "")
	guest.Close()
	if err != nil {
		return err
	}
	if err := e.stop(ctx, l); err != nil {
		return err
	}
	if strings.TrimSpace(status) == "System Integrity Protection status: disabled." {
		return nil
	}
	if strings.TrimSpace(status) != "System Integrity Protection status: enabled." {
		return domain.Err("sip_verification_failed", "SIP has a noncanonical state; inspect the image before changing policy")
	}
	folder := filepath.Join(e.Dir, "images", l.ID, "recovery-27", time.Now().UTC().Format("20060102T150405.000000000Z"))
	return e.Backend.Recovery(ctx, e.Tools.Lume, folder, l, func(ctx context.Context, endpoint string) error {
		input, _ := json.Marshal(map[string]string{"url": endpoint, "directory": folder, "tesseract": e.Tools.Tesseract, "admin_password": "lume"})
		cmd := exec.CommandContext(ctx, e.Tools.Python, "-c", recovery27Script)
		cmd.Stdin = bytes.NewReader(input)
		return lume.RunPrivate(ctx, cmd, filepath.Join(folder, "driver.log"))
	})
}
