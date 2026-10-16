package tartimage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestkeychain"
	"github.com/mallexxx/virfield/internal/tart"
)

// This harness uses only a separately journaled disposable public-base container,
// never the installed daemon's state, managed images, or another worker's VM.
func TestLiveIsolatedMacOS27ReadinessFactory(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_FACTORY") != "I_APPROVE_ISOLATED_PUBLIC_BASE_FACTORY" {
		t.Skip("requires isolated factory approval")
	}
	root := os.Getenv("VIRFIELD_LIVE_FIXTURE_DIR")
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		t.Fatal("VIRFIELD_LIVE_FIXTURE_DIR must name a private isolated fixture directory")
	}
	var journal struct{ Home, Upstream string }
	b, err := os.ReadFile(root + "/journal.json")
	if err != nil || json.Unmarshal(b, &journal) != nil || journal.Home != root+"/tart" || journal.Upstream != "ghcr.io/cirruslabs/macos-golden-gate-xcode@sha256:324ea5656dee8ab9b0a0df70fda2cfed8912051ad0eca3b1b883bf6ddac88fab" {
		t.Fatal("Expected own isolated fixture journal", err)
	}
	record := root + "/factory-journal.json"
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("Factory outcome already journaled; inspect before another run")
	}
	l := domain.Lease{ID: domain.NewID("image-"), VMName: "release-check-keychain-macos27-built", Location: "isolated", GuestUser: "admin", GuestHome: "/Users/admin", Purpose: "image"}
	p := domain.ImageProfile{Tart: &domain.TartSource{Upstream: journal.Upstream}, MacOS: "27.0", Build: "26A428", Security: "automation", Provision: "security-v1", DisableSIP: true, BuildDiskBytes: 140000000000}
	save := func(phase string) {
		data, _ := json.MarshalIndent(map[string]any{"lease": l, "profile": p, "phase": phase, "recipe_sha256": RecipeDigest()}, "", "  ")
		if err := os.WriteFile(record, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := tart.New(tart.Config{Binary: "/opt/homebrew/bin/tart", HostID: "isolated-keychain-fixture", MaxVMs: 1, Containers: []tart.ContainerConfig{{ID: "isolated", Path: journal.Home, Kind: "local"}}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := runtime.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, vm := range observation.VMs {
		if vm.State == "running" {
			t.Fatal("Stop owned isolated worker before factory run")
		}
	}
	builder := New(Config{StateDir: root + "/factory-state", Packer: "/opt/homebrew/bin/packer", TartBinary: "/opt/homebrew/bin/tart", Homes: map[string]string{"isolated": journal.Home}, Runtime: runtime})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	save("accepted")
	for _, phase := range []string{"resolve", "build", "qualify", "stop"} {
		save(phase + "-dispatched")
		t.Log("Factory stage", phase)
		if err := builder.Step(ctx, l, p, phase, func(s string) error { t.Log(s); return nil }); err != nil {
			save(phase + "-failed")
			t.Fatal(err)
		}
		save(phase + "-done")
	}
	data, err := os.ReadFile(filepath.Join(builder.folder(l), "verification.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proof struct {
		Keychain guestkeychain.Proof `json:"gui_keychain"`
	}
	if json.Unmarshal(data, &proof) != nil || !proof.Keychain.Ready || !proof.Keychain.SSHVerified {
		t.Fatal("Missing real GUI/SSH qualification evidence")
	}
	save("qualified-stopped")
	t.Log("Source factory qualified macOS 27 after reboot with native GUI/SSH readiness; own stopped image retained for journaled cleanup")
}
