package qualify

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type fakeGuest struct {
	outputs  map[string]string
	commands []string
	inputs   []string
}

func (g *fakeGuest) Run(_ context.Context, command, input string) (string, error) {
	g.commands = append(g.commands, command)
	g.inputs = append(g.inputs, input)
	for prefix, out := range g.outputs {
		if strings.HasPrefix(command, prefix) {
			return out, nil
		}
	}
	return "", domain.Err("guest_command_failed", "unexpected command")
}

func (g *fakeGuest) RunReader(ctx context.Context, _ time.Duration, command string, input io.Reader) (string, error) {
	return g.Run(ctx, command, "")
}

func code(err error) string {
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

func TestOSRequiresExactBuildAndVersion(t *testing.T) {
	g := &fakeGuest{outputs: map[string]string{"/usr/bin/sw_vers -buildVersion": "22H420\n", "/usr/bin/sw_vers -productVersion": "13.7.4\n"}}
	if v, err := OS(context.Background(), g, "13.7.4", "22H420"); err != nil || v != "13.7.4" {
		t.Fatal(v, err)
	}
	if _, err := OS(context.Background(), g, "13.7.4", "22G120"); code(err) != "guest_build_mismatch" {
		t.Fatal(err)
	}
	if _, err := OS(context.Background(), g, "13.6", "22H420"); code(err) != "guest_version_mismatch" {
		t.Fatal(err)
	}
}

func TestSIPRequiresCanonicalState(t *testing.T) {
	g := &fakeGuest{outputs: map[string]string{"/usr/bin/csrutil status": "System Integrity Protection status: disabled.\n"}}
	if _, err := SIP(context.Background(), g, true); err != nil {
		t.Fatal(err)
	}
	if _, err := SIP(context.Background(), g, false); code(err) != "sip_verification_failed" {
		t.Fatal(err)
	}
}

func TestPasswordUsesConfiguredUserAndStdin(t *testing.T) {
	g := &fakeGuest{outputs: map[string]string{"/bin/bash -c": ""}}
	if err := Password(context.Background(), g, "admin", "vf-secret"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.commands[0], "-authonly admin ") || strings.Contains(g.commands[0], "vf-secret") || g.inputs[0] != "vf-secret\n" {
		t.Fatal(g.commands, g.inputs)
	}
	if err := Password(context.Background(), g, "admin'x", "p"); code(err) != "credential_verification_failed" {
		t.Fatal(err)
	}
}

func TestXcodeProbeUsesConfiguredUser(t *testing.T) {
	x := domain.XcodeRelease{Version: "15.2", Build: "15C500b"}
	g := &fakeGuest{outputs: map[string]string{
		"/usr/libexec/PlistBuddy":      "15.2\n15C500b\n",
		"/usr/bin/codesign":            "",
		"/usr/bin/xcodebuild -version": "Xcode 15.2\nBuild version 15C500b\n",
		"/bin/bash -c":                 "ok",
	}}
	logged := []string{}
	record := func(out string) error { logged = append(logged, out); return nil }
	if err := Xcode(context.Background(), g, "admin", x, record); err != nil {
		t.Fatal(err)
	}
	probe := g.commands[len(g.commands)-1]
	if !strings.Contains(probe, "id -Gn admin") || strings.Contains(probe, "lume") || len(logged) != 1 || logged[0] != "ok" {
		t.Fatal(probe, logged)
	}
	g.outputs["/bin/bash -c"] = ""
	delete(g.outputs, "/bin/bash -c")
	if err := Xcode(context.Background(), g, "admin", x, record); code(err) != "xcode_verification_failed" || len(logged) != 2 || logged[1] != "" {
		t.Fatal("failed probe without output must still truncate the private log", err, logged)
	}
	if err := Xcode(context.Background(), g, "admin'x", x, record); code(err) != "xcode_verification_failed" {
		t.Fatal(err)
	}
}

func TestInstalledXcodeReadsBundleMetadata(t *testing.T) {
	g := &fakeGuest{outputs: map[string]string{"/usr/libexec/PlistBuddy": "27.0\n27A100\n"}}
	x, err := InstalledXcode(context.Background(), g)
	if err != nil || x.Version != "27.0" || x.Build != "27A100" {
		t.Fatal(x, err)
	}
	g.outputs["/usr/libexec/PlistBuddy"] = "garbage"
	if _, err := InstalledXcode(context.Background(), g); code(err) != "xcode_version_mismatch" {
		t.Fatal(err)
	}
}

func TestProvisionProbeUsesConfiguredUser(t *testing.T) {
	g := &fakeGuest{outputs: map[string]string{"/bin/bash -c": "assessments disabled"}}
	logged := ""
	if err := Provision(context.Background(), g, "admin", func(out string) error { logged = out; return nil }); err != nil {
		t.Fatal(err)
	}
	probe := g.commands[0]
	if !strings.Contains(probe, "id -Gn admin") || strings.Contains(probe, "id -Gn lume") || logged != "assessments disabled" {
		t.Fatal(probe, logged)
	}
	if err := Provision(context.Background(), g, "a b", func(string) error { return nil }); code(err) != "provision_verification_failed" {
		t.Fatal(err)
	}
}
