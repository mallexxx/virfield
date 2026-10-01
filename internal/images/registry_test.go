package images

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/lume"
)

func TestImportedSetupNeverRunsOfflineAccountPatch(t *testing.T) {
	// A missing imported VM must fail at guest observation, without executing
	// offline setup even on releases whose fresh installs use that path.
	for _, version := range []string{"12.6", "15.2", "27.0"} {
		t.Run(version, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/lume/host/status":
					fmt.Fprint(w, `{"vm_count":0,"max_vms":2,"available_slots":2,"status":"healthy"}`)
				case "/lume/vms":
					fmt.Fprint(w, `[]`)
				default:
					t.Errorf("unexpected backend request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			backend, err := lume.New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			engine := &Engine{Dir: t.TempDir(), Backend: backend}
			p := domain.ImageProfile{MacOS: version, Build: "24C101", Registry: &domain.RegistryReference{Source: "public", Organization: "trycua", Repository: "macos", Tag: "test", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1}}
			err = engine.Step(context.Background(), domain.Lease{ID: "image-test", VMName: "golden", Location: "home"}, p, "setup", func(string) error { return nil })
			failure, ok := err.(*domain.Error)
			if !ok || failure.Code != "vm_missing" {
				t.Fatalf("expected guest observation, got %v", err)
			}
		})
	}
}

func importedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := `{"os":"macOS","cpuCount":999,"memorySize":999999999999,"diskSize":17179869184,"macAddress":"02:11:22:33:44:55","display":"not-a-resolution","hardwareModel":"YWJj","machineIdentifier":"ZGVm","networkMode":"bridged:en0","sharedDirectories":["/Users"]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nvram.bin"), []byte("nvram"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "disk.img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(16 << 30); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return dir
}
func TestImportedVMUsesBuildBudgetAndPreservesHardware(t *testing.T) {
	dir := importedFixture(t)
	if err := normalizeImportedVM(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["cpuCount"] != float64(4) || cfg["memorySize"] != float64(8<<30) || cfg["networkMode"] != "nat" || cfg["display"] != "1920x1440" || cfg["machineIdentifier"] != "ZGVm" || cfg["hardwareModel"] != "YWJj" || cfg["sharedDirectories"] != nil {
		t.Fatal(cfg)
	}
}
func TestImportedVMRejectsInvalidDiskAndConfiguration(t *testing.T) {
	for _, name := range []string{"wrong-size", "symlink-disk", "oversized-config", "linux", "missing-identity"} {
		t.Run(name, func(t *testing.T) {
			dir := importedFixture(t)
			path := filepath.Join(dir, "config.json")
			switch name {
			case "wrong-size":
				if err := os.Truncate(filepath.Join(dir, "disk.img"), 8<<30); err != nil {
					t.Fatal(err)
				}
			case "symlink-disk":
				disk := filepath.Join(dir, "disk.img")
				if err := os.Rename(disk, disk+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(disk+".real", disk); err != nil {
					t.Fatal(err)
				}
			case "oversized-config":
				if err := os.Truncate(path, (1<<20)+1); err != nil {
					t.Fatal(err)
				}
			default:
				b, _ := os.ReadFile(path)
				var cfg map[string]any
				_ = json.Unmarshal(b, &cfg)
				if name == "linux" {
					cfg["os"] = "linux"
				} else {
					delete(cfg, "machineIdentifier")
				}
				b, _ = json.Marshal(cfg)
				if err := os.WriteFile(path, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := normalizeImportedVM(dir); err == nil {
				t.Fatal("invalid import accepted")
			}
		})
	}
}
