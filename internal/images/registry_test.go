package images

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
