package images

import (
	"context"
	_ "embed"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
)

//go:embed security.sh
var securityScript string

func (e *Engine) securityPolicy(ctx context.Context, l domain.Lease, g *guest, mode string) error {
	if mode != "apply" && mode != "verify" {
		return domain.Err("invalid_profile", "Unknown security stage")
	}
	c, err := guestssh.LoadCredentials((&guestssh.Manager{Dir: e.Dir}).CredentialPath(l))
	if err != nil {
		return err
	}
	// Password is consumed by sudo from encrypted stdin, never interpolated into
	// commands or written to stage logs. The fixed script is a shell argument so
	// a cached sudo timestamp cannot accidentally turn a password into shell input.
	command := "sudo -S -p '' /bin/bash -c " + shellQuote(securityScript) + " -- " + mode
	out, err := g.RunReader(ctx, 5*time.Minute, command, strings.NewReader(c.Password+"\n"))
	if logErr := e.provisionLog(l, "security-"+mode, out); logErr != nil {
		return logErr
	}
	if err != nil {
		return domain.Err("security_policy_failed", "Guest security "+mode+" failed; inspect the private security stage log")
	}
	if mode == "verify" {
		if _, err := g.Run(ctx, "/usr/bin/osascript -e 'tell application \"System Events\" to get name of first process'", ""); err != nil {
			return domain.Err("security_policy_failed", "Guest AppleEvents permission failed after reboot")
		}
	}
	return nil
}

func (e *Engine) provisionSecurity(ctx context.Context, l domain.Lease) error {
	vm, err := e.boot(ctx, l)
	if err != nil {
		return err
	}
	g, err := e.connect(ctx, l, vm.IP, false)
	if err != nil {
		return err
	}
	defer g.Close()
	if err := e.desktop(ctx, g); err != nil {
		return err
	}
	if err := e.securityPolicy(ctx, l, g, "apply"); err != nil {
		return err
	}
	return e.stop(ctx, l)
}
