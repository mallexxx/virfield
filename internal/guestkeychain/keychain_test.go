package guestkeychain

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type fakeGuest struct {
	command, input   string
	inputs, commands []string
	change           func(*Proof)
	sshChange        func(*Proof)
	err              error
}

func (g *fakeGuest) RunReader(_ context.Context, _ time.Duration, command string, input io.Reader) (string, error) {
	b, _ := io.ReadAll(input)
	g.command, g.input = command, string(b)
	g.inputs = append(g.inputs, g.input)
	g.commands = append(g.commands, command)
	fields := strings.Split(g.input, "\n")
	scope, nonce := fields[0], fields[1]
	if len(fields) > 3 {
		scope, nonce = fields[2], fields[4]
	}
	p := Proof{Version: 1, Scope: scope, Nonce: nonce, Boot: "11111111-1111-1111-1111-111111111111", SessionID: 100001, Aqua: true, Unlocked: true, ValueEqual: true, Ready: true}
	if g.change != nil {
		g.change(&p)
	}
	if len(fields) == 3 && g.sshChange != nil {
		g.sshChange(&p)
	}
	out, _ := json.Marshal(p)
	return string(out), g.err
}

func TestEnsureUsesPrivateStdinAndReturnsNativeGUIProof(t *testing.T) {
	g := &fakeGuest{}
	p, err := Ensure(context.Background(), g, "admin", "lease-fixture", "private-fixture-password")
	if err != nil || !p.Ready || !p.Aqua || !p.ValueEqual || !p.SSHVerified {
		t.Fatal(p, err)
	}
	if strings.Contains(g.commands[0], "private-fixture-password") || !strings.HasPrefix(g.inputs[0], "ensure\nadmin\nlease-fixture\nprivate-fixture-password\n") {
		t.Fatal("Credential did not stay on private stdin")
	}
	if _, err := Verify(context.Background(), g, "admin", "lease-fixture"); err != nil || !strings.HasPrefix(g.inputs[2], "verify\nadmin\nlease-fixture\n\n") {
		t.Fatal("Verification received an unlock password", err)
	}
}

func TestGUIUnlockDoesNotSubstituteForSSHSessionAccess(t *testing.T) {
	_, err := Ensure(context.Background(), &fakeGuest{sshChange: func(p *Proof) { p.AddStatus = -25308 }}, "admin", "lease-fixture", "private-password")
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "guest_keychain_failed" {
		t.Fatal("GUI readiness hid locked SSH session", err)
	}
}

func TestKeychainReadinessRejectsMisleadingSuccess(t *testing.T) {
	for name, change := range map[string]func(*Proof){
		"ssh session":       func(p *Proof) { p.Aqua = false },
		"locked":            func(p *Proof) { p.Unlocked = false },
		"write auth failed": func(p *Proof) { p.AddStatus = -25293 },
		"read denied":       func(p *Proof) { p.ReadStatus = -25308 },
		"different data":    func(p *Proof) { p.ValueEqual = false },
		"cleanup denied":    func(p *Proof) { p.DeleteStatus = -25308 },
		"old request":       func(p *Proof) { p.Nonce = "keychain-old" },
		"different lease":   func(p *Proof) { p.Scope = "lease-other" },
		"no audit session":  func(p *Proof) { p.SessionID = 0 },
		"clipped desktop":   func(p *Proof) { p.DisplayManaged = true; p.DisplayWidth = 1024; p.DisplayHeight = 768 },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Verify(context.Background(), &fakeGuest{change: change}, "admin", "lease-fixture")
			var failure *domain.Error
			if !errors.As(err, &failure) || failure.Code != "guest_keychain_failed" {
				t.Fatal("Misleading success was accepted", err)
			}
		})
	}
}

func TestInvalidIdentityAndTransportFailureNeverExposePassword(t *testing.T) {
	g := &fakeGuest{err: errors.New("private-fixture-password")}
	for _, args := range [][3]string{{"admin'", "lease-test", "secret"}, {"admin", "physical-host", "secret"}, {"admin", "lease-test", "secret\ninjection"}, {"admin", "lease-test", "private-fixture-password"}} {
		_, err := Ensure(context.Background(), g, args[0], args[1], args[2])
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-fixture-password") {
			t.Fatal("Invalid request or transport error leaked a credential", err)
		}
	}
}
