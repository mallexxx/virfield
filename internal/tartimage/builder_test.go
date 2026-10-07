package tartimage

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/tart"
)

type call struct {
	dir  string
	env  []string
	args []string
}

type fakeRunner struct {
	mu      sync.Mutex
	calls   []call
	err     error
	active  int
	overlap bool
}

func (r *fakeRunner) Run(_ context.Context, dir string, env []string, _ string, _ string, args ...string) error {
	r.mu.Lock()
	r.calls = append(r.calls, call{dir, env, args})
	r.active++
	if r.active > 1 {
		r.overlap = true
	}
	r.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return r.err
}

type fakeRuntime struct {
	mu          sync.Mutex
	state       string
	ip          string
	starts      int
	stops       int
	observeErrs int
}

func (f *fakeRuntime) Observe(context.Context) (domain.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.observeErrs > 0 {
		f.observeErrs--
		return domain.Observation{}, errors.New("tart list raced a clone")
	}
	if f.state == "" {
		return domain.Observation{}, nil
	}
	return domain.Observation{VMs: []domain.VM{{Name: "ventura-golden", Location: "samsung", State: f.state, IP: f.ip}}}, nil
}

func (f *fakeRuntime) Start(context.Context, domain.Lease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.state, f.ip = "running", "192.168.64.20"
	return nil
}

func (f *fakeRuntime) Stop(context.Context, domain.Lease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.state, f.ip = "stopped", ""
	return nil
}

func testBuilder(t *testing.T) (*Builder, *fakeRunner, *fakeRuntime, domain.Lease, domain.ImageProfile) {
	t.Helper()
	archives := t.TempDir()
	content := []byte("not really an xip")
	sum := sha1.Sum(content)
	p := domain.ImageProfile{
		Tart:       &domain.TartSource{Upstream: "ghcr.io/cirruslabs/macos-ventura-base@sha256:" + strings.Repeat("a", 64)},
		MacOS:      "13.7.4",
		Build:      "22H420",
		Security:   "sip-disabled",
		DisableSIP: true,
		Provision:  "developer-v1",
		Xcode:      &domain.XcodeRelease{Version: "15.2", Build: "15C500b", Requires: "13.5", SHA1: hex.EncodeToString(sum[:])},
	}
	if err := os.WriteFile(filepath.Join(archives, p.Xcode.SHA1+".xip"), content, 0600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	runtime := &fakeRuntime{}
	b := &Builder{
		config: Config{
			StateDir:      t.TempDir(),
			Packer:        "/opt/homebrew/bin/packer",
			TartBinary:    "/opt/tart/bin/tart",
			XcodeArchives: archives,
			Homes:         map[string]string{"samsung": "/Volumes/S/tart"},
			Credentials:   []tart.RegistryCredential{{Host: "ghcr.io", Username: "operator", Token: "secret"}},
			Runtime:       runtime,
		},
		run:          runner,
		stateTimeout: 200 * time.Millisecond,
		statePoll:    time.Millisecond,
	}
	l := domain.Lease{ID: "image-test", Purpose: "image", VMName: "ventura-golden", Location: "samsung", GuestUser: "admin"}
	return b, runner, runtime, l, p
}

func noProgress(string) error { return nil }

func hasCode(err error, want string) bool {
	var failure *domain.Error
	return errors.As(err, &failure) && failure.Code == want
}

func TestResolveVerifiesArchiveAndInitializesPinnedPlugin(t *testing.T) {
	b, runner, _, l, p := testBuilder(t)
	if err := b.Step(context.Background(), l, p, "resolve", noProgress); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || strings.Join(runner.calls[0].args, " ") != "init developer.pkr.hcl" {
		t.Fatal(runner.calls)
	}
	if digest, err := os.ReadFile(filepath.Join(b.config.StateDir, "images", l.ID, "recipe.sha256")); err != nil || string(digest) != RecipeDigest() {
		t.Fatal("recipe digest not recorded", err)
	}
	p.Xcode.SHA1 = strings.Repeat("0", 40)
	if err := b.Step(context.Background(), l, p, "resolve", noProgress); !hasCode(err, "unsafe_cache") {
		t.Fatal(err)
	}
}

func TestBuildPassesPinnedInputsAndScopedCredentials(t *testing.T) {
	b, runner, _, l, p := testBuilder(t)
	if err := b.Step(context.Background(), l, p, "resolve", noProgress); err != nil {
		t.Fatal(err)
	}
	if err := b.Step(context.Background(), l, p, "build", noProgress); err != nil {
		t.Fatal(err)
	}
	build := runner.calls[1]
	args := strings.Join(build.args, " ")
	for _, want := range []string{"build", "-var base=" + p.Tart.Upstream, "-var vm_name=ventura-golden", "-var macos_build=22H420", "-var sip=disabled", "-var xcode_build=15C500b", "developer.pkr.hcl"} {
		if !strings.Contains(args, want) {
			t.Fatalf("build args lack %q: %s", want, args)
		}
	}
	env := strings.Join(build.env, "\n")
	for _, want := range []string{"TART_HOME=/Volumes/S/tart", "TART_REGISTRY_HOSTNAME=ghcr.io", "TART_REGISTRY_PASSWORD=secret", "CHECKPOINT_DISABLE=1", "PACKER_PLUGIN_PATH=" + filepath.Join(b.config.StateDir, "packer-plugins"), "PATH=/opt/tart/bin:"} {
		if !strings.Contains(env, want) {
			t.Fatalf("build env lacks %q", want)
		}
	}
	runner.err = errors.New("exit status 1")
	if err := b.Step(context.Background(), l, p, "build", noProgress); !hasCode(err, "image_command_failed") {
		t.Fatal(err)
	}
}

func TestStopConfirmsStoppedVM(t *testing.T) {
	b, _, runtime, l, p := testBuilder(t)
	runtime.state = "running"
	if err := b.Step(context.Background(), l, p, "stop", noProgress); err != nil || runtime.stops != 1 {
		t.Fatal(runtime.stops, err)
	}
	if err := b.Step(context.Background(), l, p, "unknown", noProgress); !hasCode(err, "invalid_profile") {
		t.Fatal(err)
	}
}

func TestTartImagesRequireTheAdminAccount(t *testing.T) {
	b, runner, _, l, p := testBuilder(t)
	l.GuestUser = "lume"
	if err := b.Step(context.Background(), l, p, "resolve", noProgress); !hasCode(err, "invalid_profile") || len(runner.calls) != 0 {
		t.Fatal(err, runner.calls)
	}
}

func TestStopFailsFastWhenTheVMIsMissing(t *testing.T) {
	b, _, _, l, p := testBuilder(t)
	b.stateTimeout = time.Hour
	if err := b.Step(context.Background(), l, p, "stop", noProgress); !hasCode(err, "vm_missing") {
		t.Fatal(err)
	}
}

func TestStateWaitsSurviveTransientObserveErrors(t *testing.T) {
	b, _, runtime, l, _ := testBuilder(t)
	runtime.state, runtime.observeErrs = "stopped", 3
	if err := b.waitState(context.Background(), l, "stopped"); err != nil {
		t.Fatal(err)
	}
	runtime.observeErrs = 1 << 30
	if err := b.waitState(context.Background(), l, "running"); !hasCode(err, "guest_timeout") {
		t.Fatal(err)
	}
}

// Concurrent image jobs share one plugin directory; overlapping installs
// left a starting build with "Missing plugins".
func TestPluginInitializationIsSerialized(t *testing.T) {
	b, runner, _, l, p := testBuilder(t)
	other := l
	other.ID = "image-other"
	var wg sync.WaitGroup
	for _, lease := range []domain.Lease{l, other} {
		wg.Add(1)
		go func(lease domain.Lease) {
			defer wg.Done()
			if err := b.Step(context.Background(), lease, p, "resolve", noProgress); err != nil {
				t.Error(err)
			}
		}(lease)
	}
	wg.Wait()
	if runner.overlap || len(runner.calls) != 2 {
		t.Fatal("packer init ran concurrently", len(runner.calls))
	}
}
