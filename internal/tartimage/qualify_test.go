package tartimage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestkeychain"
)

type fakeSession struct {
	runtime        *fakeRuntime
	commands       *[]string
	build          string
	noShutdown     bool
	keychainLocked bool
}

func (s fakeSession) Run(_ context.Context, command, _ string) (string, error) {
	*s.commands = append(*s.commands, command)
	switch {
	case strings.HasPrefix(command, "/usr/bin/sw_vers -buildVersion"):
		if s.build != "" {
			return s.build + "\n", nil
		}
		return "22H420\n", nil
	case strings.HasPrefix(command, "/usr/bin/sw_vers -productVersion"):
		return "13.7.4\n", nil
	case strings.HasPrefix(command, "/usr/bin/csrutil status"):
		return "System Integrity Protection status: disabled.\n", nil
	case strings.HasPrefix(command, "if /usr/bin/pgrep"):
		return "desktop\n", nil
	case strings.Contains(command, "diskutil info"):
		return "false\ntrue\nfalse\nfalse\ntrue\nfalse\n", nil
	case strings.HasPrefix(command, "/usr/libexec/PlistBuddy"):
		return "15.2\n15C500b\n", nil
	case strings.HasPrefix(command, "/usr/bin/xcodebuild -version"):
		return "Xcode 15.2\nBuild version 15C500b\n", nil
	case strings.HasPrefix(command, "sudo -n /bin/bash -s -- verify"):
		return "Guest policy verify complete; SIP disabled; Gatekeeper disabled", nil
	case strings.Contains(command, "kern.bootargs"):
		return "assessments disabled\n", nil
	case strings.HasPrefix(command, "sudo -n /sbin/shutdown"):
		if !s.noShutdown {
			s.runtime.mu.Lock()
			s.runtime.state, s.runtime.ip = "stopped", ""
			s.runtime.mu.Unlock()
		}
		return "", nil
	}
	return "", nil
}

func (s fakeSession) RunReader(ctx context.Context, _ time.Duration, command string, input io.Reader) (string, error) {
	if strings.Contains(command, "ai.virfield.guest-keychain") {
		*s.commands = append(*s.commands, "gui-keychain")
		b, _ := io.ReadAll(input)
		fields := strings.Split(string(b), "\n")
		scope, nonce := fields[0], fields[1]
		if len(fields) > 3 {
			scope, nonce = fields[2], fields[4]
		}
		out, _ := json.Marshal(guestkeychain.Proof{Version: 1, Scope: scope, Nonce: nonce, Boot: "boot-fixture", SessionID: 123, Aqua: true, Unlocked: !s.keychainLocked, ValueEqual: true, Ready: true, DesktopSnapshotAvailable: true, ConsoleReady: true})
		return string(out), nil
	}
	return s.Run(ctx, command, "")
}

func (fakeSession) Close() {}

type fakeGuests struct {
	runtime        *fakeRuntime
	commands       []string
	connects       []bool
	secured        int
	build          string
	noShutdown     bool
	keychainLocked bool
}

func (g *fakeGuests) Connect(_ context.Context, _ domain.Lease, _ string, bootstrap bool) (session, error) {
	g.connects = append(g.connects, bootstrap)
	return fakeSession{runtime: g.runtime, commands: &g.commands, build: g.build, noShutdown: g.noShutdown, keychainLocked: g.keychainLocked}, nil
}

func TestQualifyRefusesLockedGUIKeychainAfterReboot(t *testing.T) {
	b, rt, guests, l, p := qualifyBuilder(t)
	guests.keychainLocked = true
	err := b.Step(context.Background(), l, p, "qualify", func(string) error { return nil })
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "guest_keychain_failed" {
		t.Fatal("Locked GUI keychain was accepted", err)
	}
	if _, err := os.Stat(filepath.Join(b.folder(l), "verification.json")); !os.IsNotExist(err) {
		t.Fatal("Failed keychain verification published evidence", err)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.state != "stopped" {
		t.Fatal("Failed image qualification left the VM running")
	}
}

func (g *fakeGuests) Secure(context.Context, domain.Lease, string, session) error {
	g.secured++
	return nil
}

func (g *fakeGuests) Password(domain.Lease) (string, error) { return "vf-rotated", nil }

func qualifyBuilder(t *testing.T) (*Builder, *fakeRuntime, *fakeGuests, domain.Lease, domain.ImageProfile) {
	t.Helper()
	b, _, runtime, l, p := testBuilder(t)
	b.stateTimeout = 5 * time.Second
	runtime.state = "stopped"
	g := &fakeGuests{runtime: runtime}
	b.guests = g
	b.deviceSupport = func(context.Context, string, string, string, string, func(string) error) (string, error) {
		return "/Users/x/Library/Developer/Xcode/macOS DeviceSupport/13.7.4 (22H420)/Symbols", nil
	}
	if err := os.MkdirAll(b.folder(l), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.folder(l), "recipe.sha256"), []byte(RecipeDigest()), 0600); err != nil {
		t.Fatal(err)
	}
	return b, runtime, g, l, p
}

func TestQualifyFailureStopsTheImageVM(t *testing.T) {
	b, runtime, g, l, p := qualifyBuilder(t)
	g.build = "22G120"
	if err := b.Step(context.Background(), l, p, "qualify", noProgress); !hasCode(err, "guest_build_mismatch") {
		t.Fatal(err)
	}
	if runtime.state != "stopped" {
		t.Fatal("failed qualification left the image VM running")
	}
}

func TestShutdownFallsBackToRuntimeStop(t *testing.T) {
	b, runtime, g, l, _ := qualifyBuilder(t)
	b.stateTimeout = 100 * time.Millisecond
	g.noShutdown = true
	runtime.state = "running"
	session, _ := g.Connect(context.Background(), l, "", false)
	if err := b.shutdown(context.Background(), l, session); err != nil || runtime.stops != 1 {
		t.Fatal(runtime.stops, err)
	}
}

func TestQualifySecuresRebootsVerifiesAndRecordsEvidence(t *testing.T) {
	b, runtime, g, l, p := qualifyBuilder(t)
	if err := b.Step(context.Background(), l, p, "qualify", noProgress); err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 2 || runtime.state != "stopped" || g.secured != 1 || len(g.connects) != 2 || !g.connects[0] || g.connects[1] {
		t.Fatalf("starts=%d state=%s secured=%d connects=%v", runtime.starts, runtime.state, g.secured, g.connects)
	}
	joined := strings.Join(g.commands, "\n")
	if !strings.Contains(joined, "-authonly admin ") || !strings.Contains(joined, "id -Gn admin") {
		t.Fatal("qualification did not use the template guest user")
	}
	var evidence map[string]any
	raw, err := os.ReadFile(filepath.Join(b.folder(l), "verification.json"))
	if err != nil || json.Unmarshal(raw, &evidence) != nil {
		t.Fatal(err)
	}
	if evidence["upstream"] != p.Tart.Upstream || evidence["recipe_sha256"] != RecipeDigest() || evidence["build"] != "22H420" {
		t.Fatal(evidence)
	}
}

func TestUITestQualifyRecordsInstalledXcodeAndVerifiesProvisioning(t *testing.T) {
	b, _, g, l, _ := qualifyBuilder(t)
	g.build = "26A428"
	p := uitestProfile()
	p.MacOS = "13.7.4" // the fake session reports 13.7.4
	if err := b.Step(context.Background(), l, p, "qualify", noProgress); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(g.commands, "\n")
	if !strings.Contains(joined, "kern.bootargs") || !strings.Contains(joined, "id -Gn admin") {
		t.Fatal("UI-test provisioning was not verified for the admin account")
	}
	raw, err := os.ReadFile(filepath.Join(b.folder(l), "verification.json"))
	if err != nil || !strings.Contains(string(raw), `"version": "15.2"`) {
		t.Fatal("installed Xcode not recorded", err)
	}
}

func TestBaseQualificationDoesNotRequireXcodeOrDeviceSupport(t *testing.T) {
	b, _, g, l, p := qualifyBuilder(t)
	p.Xcode = nil
	p.Provision = "base-v1"
	b.deviceSupport = func(context.Context, string, string, string, string, func(string) error) (string, error) {
		t.Fatal("base requested Xcode DeviceSupport")
		return "", nil
	}
	if err := b.Step(context.Background(), l, p, "qualify", noProgress); err != nil {
		t.Fatal(err)
	}
	for _, command := range g.commands {
		if strings.Contains(command, "Xcode.app") || strings.Contains(command, "xcodebuild") {
			t.Fatal("base unexpectedly requires Xcode", command)
		}
	}
}
func TestPortableQualificationPreservesPublicBootstrapAccount(t *testing.T) {
	b, _, g, l, p := qualifyBuilder(t)
	l.Portable = true
	if err := b.Step(context.Background(), l, p, "qualify", noProgress); err != nil {
		t.Fatal(err)
	}
	if g.secured != 0 || len(g.connects) != 2 || !g.connects[0] || !g.connects[1] {
		t.Fatal("portable rebuild received private golden credentials", g.secured, g.connects)
	}
	raw, err := os.ReadFile(filepath.Join(b.folder(l), "verification.json"))
	if err != nil || !strings.Contains(string(raw), `"scoped_image_ssh": false`) {
		t.Fatal(string(raw), err)
	}
}

func TestAutomationProfileIsVerifiedAfterReboot(t *testing.T) {
	b, _, g, l, p := qualifyBuilder(t)
	p.Security = "automation"
	if err := b.Step(context.Background(), l, p, "qualify", noProgress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(g.commands, "\n"), "sudo -n /bin/bash -s -- verify 'admin'") {
		t.Fatal("automation policy was not checked after reboot")
	}
}
