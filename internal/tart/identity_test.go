package tart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeIdentitySurvivesDisplayAndSerializationButRejectsReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	config := map[string]any{"ecid": "stable-machine-id", "hardwareModel": "virtual-hardware-model", "macAddress": "02:00:00:00:00:01", "os": "darwin", "arch": "arm64", "version": 1, "display": map[string]any{"width": 1920}, "cpuCount": 4}
	write := func() {
		t.Helper()
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	before := runtimeIdentity(path)
	if before == "" {
		t.Fatal("Complete hardware identity unavailable")
	}
	config["display"] = map[string]any{"width": 1280}
	config["cpuCount"] = 6
	write()
	if runtimeIdentity(path) != before {
		t.Fatal("Presentation/resource settings replaced hardware identity")
	}
	config["ecid"] = "replacement-machine"
	write()
	if runtimeIdentity(path) == before || runtimeIdentity(path) == "" {
		t.Fatal("Same-name replacement did not change identity")
	}
	config["ecid"] = ""
	write()
	if runtimeIdentity(path) != "" {
		t.Fatal("Missing ECID was invented")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "actual.json")
	if err := os.WriteFile(other, []byte(`{"ecid":"x","hardwareModel":"y","macAddress":"02:00:00:00:00:01"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if runtimeIdentity(path) != "" {
		t.Fatal("Followed a replaced config symlink")
	}
}
