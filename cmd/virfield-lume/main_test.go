package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestParseStorageLocations(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got := parseStorageLocations([]byte("VM Storage Locations:\n  - home: ~/.lume (default)\n  - samsung: /Volumes/Samsung/virfield\n"))
	if got["home"] != filepath.Join(home, ".lume") || got["samsung"] != "/Volumes/Samsung/virfield" {
		t.Fatalf("parsed locations: %#v", got)
	}
}

func TestSyncStorageLocationsAddsOnlyAvailableMissingPaths(t *testing.T) {
	dir := t.TempDir()
	external := filepath.Join(dir, "external")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "lume")
	body := "#!/bin/sh\n" +
		"if [ \"$1 $2 $3\" = \"config storage list\" ]; then printf '%s\\n' '  - home: " + dir + "/home (default)'; exit 0; fi\n" +
		"printf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	configured := map[string]string{"home": filepath.Join(dir, "home"), "external": external, "offline": filepath.Join(dir, "offline")}
	if err := syncStorageLocations(context.Background(), script, configured); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(calls))
	want := "config storage add external " + external
	if got != want {
		t.Fatalf("calls %q, want %q", got, want)
	}
}

func TestSyncStorageLocationsRejectsPathMismatch(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "lume")
	body := "#!/bin/sh\nprintf '%s\\n' '  - home: /unexpected (default)'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syncStorageLocations(context.Background(), script, map[string]string{"home": filepath.Join(dir, "home")}); err == nil {
		t.Fatal("expected path mismatch")
	}
}
