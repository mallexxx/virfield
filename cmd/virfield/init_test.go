package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mallexxx/virfield/internal/config"
)

func TestInitCreatesExplicitTartConfigAndPreservesExistingDeployment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new deployment")
	if err := initialize(dir, "golden", "golden", "local"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil || cfg.Runtime.Provider != "tart" || cfg.LumeURL != "" || len(cfg.LegacyImageTools) != 0 {
		t.Fatal(cfg, err)
	}
	token, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := initialize(dir, "golden", "golden", "local"); err == nil {
		t.Fatal("existing deployment overwritten")
	}
	after, err := os.ReadFile(cfg.TokenFile)
	if err != nil || string(after) != string(token) {
		t.Fatal("existing token rotated", err)
	}
}
