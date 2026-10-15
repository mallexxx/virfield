package legacyios

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type probeGuest struct {
	calls  int
	output string
	err    error
}

func TestNativeArchitectureCheckUsesRealAppleLipo(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple Mach-O tool")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	native, foreign := "arm64", "x86_64"
	if runtime.GOARCH == "amd64" {
		native, foreign = foreign, native
	}
	script, err := assets.ReadFile("assets/native_arch.rb")
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "native_arch.rb")
	if err := os.WriteFile(helper, script, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/bin/ruby", helper, binary, native).CombinedOutput(); err != nil {
		t.Fatal("Real native Mach-O rejected", err, string(out))
	}
	if out, err := exec.Command("/usr/bin/ruby", helper, binary, foreign).CombinedOutput(); err == nil {
		t.Fatal("Foreign architecture accepted", string(out))
	}
	plain := filepath.Join(t.TempDir(), "not-mach-o")
	if err := os.WriteFile(plain, []byte("ordinary text"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/bin/ruby", helper, plain, native).CombinedOutput(); err == nil {
		t.Fatal("Non Mach-O input accepted", string(out))
	}
}

func TestProfileEnvironmentNormalizesNoninteractiveCLocale(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system Ruby locale probe")
	}
	data, err := assets.ReadFile("assets/env.sh")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "profile-env.sh")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := `. "$1"; exec /usr/bin/ruby --disable-gems -e 'abort "wrong external encoding" unless Encoding.default_external == Encoding::UTF_8; File.expand_path(".").unicode_normalize(:nfc); abort "unfrozen tools" unless ENV["BUNDLE_FROZEN"] == "true"'`
	cmd := exec.Command("/bin/bash", "-eu", "-c", command, "profile-test", file)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal("Noninteractive C locale did not become usable UTF-8", err, string(out))
	}
}

func (g *probeGuest) Run(context.Context, string, string) (string, error) {
	g.calls++
	return "", errors.New("unexpected short command")
}
func (g *probeGuest) RunReader(context.Context, time.Duration, string, io.Reader) (string, error) {
	g.calls++
	return g.output, g.err
}
func profile() domain.ImageProfile {
	return domain.ImageProfile{Tart: &domain.TartSource{Upstream: "ghcr.io/team/base@sha256:" + strings.Repeat("a", 64)}, MacOS: "12.7.6", Build: "21H1320", Provision: "developer-v1", Xcode: &domain.XcodeRelease{Version: "13.4.1", Build: "13F100", Requires: "12.0", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip", SHA1: strings.Repeat("b", 40)}, Toolchain: Name, ToolchainSHA256: Digest()}
}
func TestOrdinaryAndMismatchedProfilesNeverExecute(t *testing.T) {
	g := &probeGuest{}
	log := func(string) error { t.Fatal("ordinary profile wrote toolchain log"); return nil }
	if err := Install(context.Background(), g, domain.ImageProfile{}, log); err != nil {
		t.Fatal(err)
	}
	if v, err := Verify(context.Background(), g, domain.ImageProfile{}, log); err != nil || v != nil {
		t.Fatal(v, err)
	}
	p := profile()
	p.ToolchainSHA256 = strings.Repeat("0", 64)
	if err := Install(context.Background(), g, p, log); err == nil {
		t.Fatal("changed recipe accepted")
	}
	if g.calls != 0 {
		t.Fatal("unselected or mismatched recipe executed")
	}
}
func TestVerificationNeedsCompletePinnedEvidenceAndBuild(t *testing.T) {
	good := `{"profile":"legacy-ios-v1","ruby":"3.3.12","cocoapods":"1.16.2","ffi":"1.17.4","ethon":"0.18.0","architecture":"arm64"}` + "\nvirfield-legacy-ios-armv7-ok\n"
	for _, tc := range []struct {
		name, out string
		err       error
		ok        bool
	}{
		{"complete", good, nil, true}, {"missing build", strings.Split(good, "\n")[0], nil, false}, {"wrong Ruby", strings.Replace(good, "3.3.12", "2.6.10", 1), nil, false}, {"wrong architecture", strings.Replace(good, "arm64", "java", 1), nil, false}, {"build failure", good, errors.New("build failed"), false}, {"empty evidence", "virfield-legacy-ios-armv7-ok", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &probeGuest{output: tc.out, err: tc.err}
			logged := ""
			v, err := Verify(context.Background(), g, profile(), func(s string) error { logged = s; return nil })
			if (err == nil) != tc.ok || logged != tc.out {
				t.Fatal(v, err)
			}
			if tc.ok && (v["armv7_build"] != true || v["recipe_sha256"] != Digest()) {
				t.Fatal(v)
			}
		})
	}
}
func TestSupplyChainLockAndPayloadAreComplete(t *testing.T) {
	var manifest struct {
		Gems []struct {
			Name     string `json:"name"`
			Version  string `json:"version"`
			URL      string `json:"url"`
			SHA256   string `json:"sha256"`
			Metadata string `json:"metadata"`
		}
		Sources []struct {
			Name   string `json:"name"`
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		}
	}
	data, _ := assets.ReadFile("assets/manifest.json")
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	lock, _ := assets.ReadFile("assets/Gemfile.lock")
	matches := regexp.MustCompile(`(?m)^    ([A-Za-z0-9_-]+) \(([^ )]+)\)$`).FindAllStringSubmatch(string(lock), -1)
	if len(matches) != len(manifest.Gems) || len(manifest.Gems) < 40 || len(manifest.Sources) != 3 {
		t.Fatal("incomplete transitive lock")
	}
	pins := map[string]string{}
	for _, g := range manifest.Gems {
		if !strings.HasSuffix(g.Metadata, "?platform=ruby") || g.URL != "https://rubygems.org/downloads/"+g.Name+"-"+g.Version+".gem" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(g.SHA256) {
			t.Fatal(g)
		}
		pins[g.Name] = g.Version
	}
	for _, m := range matches {
		if pins[m[1]] != m[2] {
			t.Fatal("lock differs from hash manifest", m)
		}
	}
	payload, err := payload()
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(payload))
	seen := map[string]bool{}
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if path.Clean(h.Name) != h.Name || strings.HasPrefix(h.Name, "/") || strings.HasPrefix(h.Name, "../") {
			t.Fatal("unsafe payload path", h.Name)
		}
		seen[h.Name] = true
		if h.Name == "profile.sha256" {
			b, _ := io.ReadAll(reader)
			if string(b) != Digest()+"\n" {
				t.Fatal("recipe digest not bound")
			}
		}
	}
	for _, name := range []string{"Gemfile", "Gemfile.lock", "fixture/Podfile.lock", "fixture/project.rb", "profile.sha256", "manifest.json", "downloads.tsv", "install.sh", "verify.sh", "verify.rb", "env.sh", "native_arch.rb"} {
		if !seen[name] {
			t.Fatal("missing payload", name)
		}
	}
	for _, name := range []string{"install.sh", "verify.sh", "check-platform.sh"} {
		data, _ := assets.ReadFile("assets/" + name)
		cmd := exec.Command("/bin/bash", "-n")
		cmd.Stdin = bytes.NewReader(data)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(name, err, string(out))
		}
	}
}
