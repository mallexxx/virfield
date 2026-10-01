package lume

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			err = RegistryPush(context.Background(), binary, l, s, r.Repository, r.Tag, "fixture-secret", log)
		} else {
			err = RegistryPull(context.Background(), binary, l, s, r, "fixture-secret", log)
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
	if err := RegistryPush(context.Background(), binary, l, s, "vm", "v1", "secret", filepath.Join(dir, "rejected.log")); err == nil {
		t.Fatal("raw managed VM export allowed")
	}
}
