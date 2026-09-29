package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Token(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Token(path); err == nil {
		t.Fatal("world-readable token accepted")
	}
}
func TestListenAndAbsolutePaths(t *testing.T) {
	for _, body := range []string{`{"listen":"0.0.0.0:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":2}`, `{"listen":"127.0.0.1:7780","state_dir":"relative","token_file":"/tmp/t","max_vms":2}`, `{"listen":"127.0.0.1:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":3}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("unsafe configuration accepted", body)
		}
	}
}
