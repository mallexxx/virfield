package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBinaryPrefersExplicitAbsoluteBinary(t *testing.T) {
	got, err := resolveBinary("/opt/virfield/lume/lume", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/opt/virfield/lume/lume" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveBinaryReadsLumeFromConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	lume := filepath.Join(dir, "tools", "lume")
	if err := os.WriteFile(configPath, []byte(`{
		"listen":"127.0.0.1:7780",
		"lume_url":"http://127.0.0.1:7777",
		"state_dir":"`+dir+`",
		"token_file":"`+filepath.Join(dir, "token")+`",
		"max_vms":1,
		"templates":[],
		"image_tools":{
			"lume":"`+lume+`",
			"python":"`+filepath.Join(dir, "python")+`",
			"tesseract":"`+filepath.Join(dir, "tesseract")+`",
			"vnc_bin":"`+dir+`"
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveBinary("", configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != lume {
		t.Fatalf("got %q, want %q", got, lume)
	}
}

func TestResolveBinaryRejectsUnstableRelativeInputs(t *testing.T) {
	if _, err := resolveBinary("tools/lume", ""); err == nil {
		t.Fatal("expected relative binary to fail")
	}
	if _, err := resolveBinary("", "config.json"); err == nil {
		t.Fatal("expected relative config path to fail")
	}
}
