package tartimage

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type fakeSession struct {
	runtime    *fakeRuntime
	commands   *[]string
	build      string
	noShutdown bool
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

func (s fakeSession) RunReader(ctx context.Context, _ time.Duration, command string, _ io.Reader) (string, error) {
	return s.Run(ctx, command, "")
}

func (fakeSession) Close() {}

type fakeGuests struct {
	runtime    *fakeRuntime
	commands   []string
	connects   []bool
	secured    int
	build      string
	noShutdown bool
}

func (g *fakeGuests) Connect(_ context.Context, _ domain.Lease, _ string, bootstrap bool) (session, error) {
	g.connects = append(g.connects, bootstrap)
	return fakeSession{runtime: g.runtime, commands: &g.commands, build: g.build, noShutdown: g.noShutdown}, nil
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
