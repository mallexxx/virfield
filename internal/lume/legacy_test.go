package lume

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestLegacyClonePreservesMachineIdentityBeforeBoot(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "golden")
	cloneDir := filepath.Join(root, "vf-worker")
	if err := os.Mkdir(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	source := map[string]any{"machineIdentifier": "source-machine", "macAddress": "aa:bb:cc:dd:ee:ff", "hardwareModel": "mac-model", "diskSize": 85899345920, "os": "macOS", "display": "1920x1440"}
	clone := map[string]any{"machineIdentifier": "new-machine", "macAddress": "11:22:33:44:55:66", "hardwareModel": "mac-model", "diskSize": 85899345920, "os": "macOS", "display": "1920x1440"}
	write := func(dir string, config map[string]any) {
		b, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "nvram.bin"), []byte("copied nvram"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(sourceDir, source)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lume/vms/clone" {
			t.Error(r.URL.Path)
		}
		if err := os.Mkdir(cloneDir, 0700); err != nil {
			t.Error(err)
		}
		write(cloneDir, clone)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.StoragePaths = map[string]string{"home": root}
	legacy := "123e4567-e89b-12d3-a456-426614174000"
	if err := c.Clone(context.Background(), domain.Template{Name: "golden", Location: "home", LegacyUUID: legacy}, domain.Lease{VMName: "vf-worker", Location: "home", LegacyUUID: legacy}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(cloneDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["machineIdentifier"] != source["machineIdentifier"] || got["macAddress"] != source["macAddress"] || got["hardwareModel"] != clone["hardwareModel"] || got["display"] != clone["display"] {
		t.Fatalf("legacy clone configuration: %#v", got)
	}
	if b, err := os.ReadFile(filepath.Join(cloneDir, "nvram.bin")); err != nil || string(b) != "copied nvram" {
		t.Fatalf("cloned NVRAM: %q, %v", b, err)
	}
}

func TestLegacyCloneRejectsMissingCopiedNVRAM(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"golden", "vf-worker"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "golden", "nvram.bin"), []byte("nvram"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &Client{StoragePaths: map[string]string{"home": root}}
	legacy := "123e4567-e89b-12d3-a456-426614174000"
	if err := c.preserveLegacyIdentity(domain.Template{Name: "golden", Location: "home", LegacyUUID: legacy}, domain.Lease{VMName: "vf-worker", Location: "home", LegacyUUID: legacy}); err == nil {
		t.Fatal("legacy clone without copied NVRAM accepted")
	}
}
