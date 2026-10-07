package config

import (
	"fmt"
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
	for _, body := range []string{`{"listen":"0.0.0.0:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":2}`, `{"listen":"127.0.0.1:7780","state_dir":"relative","token_file":"/tmp/t","max_vms":2}`, `{"listen":"127.0.0.1:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":1025}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("unsafe configuration accepted", body)
		}
	}
}
func TestPrincipalScopesMustBeExplicit(t *testing.T) {
	base := `{"listen":"127.0.0.1:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":2,"principals":`
	for _, candidate := range []struct {
		value   string
		allowed bool
	}{
		{`[{"name":"broker","token_file":"/tmp/broker","scopes":["lease:own","image:build"]}]`, true},
		{`[{"name":"broker","token_file":"/tmp/broker","scopes":["operator"]}]`, false},
		{`[{"name":"broker","token_file":"relative","scopes":["lease:own"]}]`, false},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(base+candidate.value+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if (err == nil) != candidate.allowed {
			t.Fatal(candidate.value, err)
		}
	}
}

func TestCloneLocationsReferenceConfiguredStorage(t *testing.T) {
	base := `{"listen":"127.0.0.1:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":2,"storage_paths":{"home":"/tmp/home","external":"/tmp/external"},`
	for _, candidate := range []struct {
		value   string
		allowed bool
	}{
		{`"default_clone_location":"external","fallback_clone_locations":["home"]}`, true},
		{`"default_clone_location":"missing"}`, false},
		{`"default_clone_location":"home","fallback_clone_locations":["home"]}`, false},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(base+candidate.value), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if (err == nil) != candidate.allowed {
			t.Fatal(candidate.value, err)
		}
	}
}

func TestTartRuntimeDefinesDurableStorageAndReserve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"listen":"127.0.0.1:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":2,"runtime":{"provider":"tart","tart":{"binary":"/opt/homebrew/bin/tart","host_id":"macbook","containers":[{"id":"local","path":"/Users/admin/.tart","kind":"local"},{"id":"samsung","path":"/Volumes/Samsung/tart","kind":"external","volume_uuid":"123e4567-e89b-12d3-a456-426614174000"}]}},"templates":[]}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Tart.MemoryReserveBytes != 4<<30 || cfg.Runtime.Tart.DiskReserveBytes != 20<<30 || cfg.StoragePaths["samsung"] != "/Volumes/Samsung/tart" {
		t.Fatalf("runtime defaults not applied: %#v", cfg.Runtime.Tart)
	}
}

func TestOrchardHostLimitsRequireExplicitReserve(t *testing.T) {
	for _, limit := range []string{
		`"mac-one":{"max_vms":2,"memory_reserve_bytes":4294967296}`,
		`"mac-one":{"max_vms":2,"memory_reserve_bytes":0}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		body := `{"listen":"127.0.0.1:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":4,"runtime":{"provider":"orchard","orchard":{"url":"https://orchard.example","service_account":"virfield","token_file":"/tmp/orchard-token","pool_id":"personal","host_limits":{` + limit + `}}},"templates":[]}`
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if (err == nil) != !strings.Contains(limit, `:0}`) {
			t.Fatalf("limit %s: %v", limit, err)
		}
	}
}

func TestTartImageToolsRequireAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	base := `{"listen":"127.0.0.1:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":2,"runtime":{"provider":"tart","tart":{"binary":"/opt/homebrew/bin/tart","host_id":"macbook","containers":[{"id":"samsung","path":"/Volumes/S/tart","kind":"external"}],"image":%s}},"templates":[]}`
	for name, image := range map[string]string{
		"valid":            `{"packer":"/opt/homebrew/bin/packer","xcode_archives":"/Users/a/cache/xcode"}`,
		"relative packer":  `{"packer":"packer","xcode_archives":"/Users/a/cache/xcode"}`,
		"missing archives": `{"packer":"/opt/homebrew/bin/packer"}`,
	} {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(fmt.Sprintf(base, image)), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if name == "valid" {
			if err != nil || cfg.Runtime.Tart.Image.Packer != "/opt/homebrew/bin/packer" {
				t.Fatal(cfg, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
