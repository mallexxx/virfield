package lume

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestRegistryCommandUsesScopedEnvironmentAndPrivateLog(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "fixture")
	script := `#!/bin/sh
printf 'ARGS:'
printf '<%s>' "$@"
printf '\nUSER:%s\nTOKEN:%s\nUNRELATED:%s\n' "$GITHUB_USERNAME" "$GITHUB_TOKEN" "$VIRFIELD_TEST_UNRELATED_SECRET"
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIRFIELD_TEST_UNRELATED_SECRET", "must-not-inherit")
	t.Setenv("GHCR_TOKEN", "must-not-inherit-either")
	s := domain.RegistrySource{ID: "team", Organization: "team", Username: "user", TokenFile: filepath.Join(dir, "token"), AllowPush: true}
	r := domain.RegistryReference{Source: "team", Organization: "team", Repository: "vm", Tag: "v1", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1024}
	l := domain.Lease{ID: "image-abc", VMName: "vf-export-abc", Location: "home", Portable: true}
	for _, push := range []bool{false, true} {
		log := filepath.Join(dir, map[bool]string{false: "pull.log", true: "push.log"}[push])
		var err error
		if push {
			err = RegistryPush(context.Background(), binary, l, s, r.Repository, r.Tag, "fixture-secret", log, dir)
		} else {
			err = RegistryPull(context.Background(), binary, l, s, r, "fixture-secret", log, dir)
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(log)
		out := string(b)
		first := strings.SplitN(out, "\n", 2)[0]
		if strings.Contains(first, "secret") || strings.Contains(first, "--password") || strings.Contains(out, "must-not-inherit") || !strings.Contains(out, "TOKEN:fixture-secret") || !strings.Contains(first, "<--registry><ghcr.io>") {
			t.Fatal("registry subprocess boundary failed")
		}
		st, _ := os.Stat(log)
		if st.Mode().Perm() != 0600 {
			t.Fatal(st.Mode())
		}
	}
	l.Portable = false
	if err := RegistryPush(context.Background(), binary, l, s, "vm", "v1", "secret", filepath.Join(dir, "rejected.log"), dir); err == nil {
		t.Fatal("raw managed VM export allowed")
	}
}

func TestRegistrySettingsDoNotInheritHostRegistryOrStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "/unrelated/operator/settings")
	env, cleanup, err := registryEnvironment(domain.Lease{Location: "custom"}, domain.RegistrySource{ID: "public", Organization: "trycua"}, "", filepath.Join(dir, "private logs", "pull.log"), filepath.Join(dir, "VM storage"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	settings := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "XDG_CONFIG_HOME=") {
			settings = strings.TrimPrefix(entry, "XDG_CONFIG_HOME=")
		}
	}
	if settings == "" || settings == "/unrelated/operator/settings" {
		t.Fatal("host config inherited")
	}
	b, err := os.ReadFile(filepath.Join(settings, "lume", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`defaultLocationName: "custom"`, `registry: "ghcr.io"`, `organization: "trycua"`, `telemetryEnabled: false`, `cachingEnabled: false`, filepath.Join(dir, "VM storage")} {
		if !strings.Contains(string(b), want) {
			t.Fatal("missing isolated setting", want)
		}
	}
	cleanup()
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatal("private settings not removed")
	}
}

func TestLiveRegistrySettingsWithPinnedLume(t *testing.T) {
	binary := os.Getenv("VIRFIELD_LIVE_REGISTRY_LUME_BINARY")
	if binary == "" {
		t.Skip("read-only config probe needs pinned Lume binary")
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "VM storage")
	if err := os.Mkdir(storage, 0700); err != nil {
		t.Fatal(err)
	}
	env, cleanup, err := registryEnvironment(domain.Lease{Location: "custom"}, domain.RegistrySource{ID: "public", Organization: "trycua"}, "", filepath.Join(dir, "logs", "pull.log"), storage)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "config", "get")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("pinned Lume config probe failed", err)
	}
	for _, want := range []string{"Default VM storage: custom (" + storage + ")", "Registry type: ghcr", "Registry: ghcr.io/trycua", "Telemetry enabled: false"} {
		if !strings.Contains(string(out), want) {
			t.Fatal("pinned Lume did not respect isolated configuration", want)
		}
	}
}
