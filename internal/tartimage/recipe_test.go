package tartimage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/guestscripts"
)

// compact folds whitespace so assertions do not depend on HCL alignment.
func compact(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestRecipePinsPluginAndGuestPolicies(t *testing.T) {
	if len(RecipeDigest()) != 64 {
		t.Fatal("invalid recipe digest")
	}
	template, err := recipe.ReadFile("recipe/developer.pkr.hcl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`version = "= 1.21.0"`, `recovery_partition = "relocate"`, "disk_size_gb       = 120", `script = "scripts/reset-host-keys.sh"`, `script = "scripts/grow-disk.sh"`} {
		if !strings.Contains(compact(string(template)), compact(want)) {
			t.Fatalf("recipe lacks %s", want)
		}
	}
}

func TestRecipeScriptsParseAndMaterializePrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recipe")
	if err := writeRecipe(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check-guest.sh", "install-xcode.sh", "xcode-post.sh", "reset-host-keys.sh", "grow-disk.sh", "security.sh", "provision27.sh"} {
		path := filepath.Join(dir, "scripts", name)
		if out, err := exec.Command("/bin/bash", "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("%s: %s %v", name, out, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("%s is not private: %v %v", name, info.Mode(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "developer.pkr.hcl")); err != nil {
		t.Fatal(err)
	}
}

func TestUITestRecipeAdoptsUpstreamXcodeAndRunsSharedRecipe(t *testing.T) {
	uitest, err := recipe.ReadFile("recipe/uitest.pkr.hcl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vm_base_name       = var.base", `script = "scripts/adopt-xcode.sh"`, `script           = "scripts/provision27.sh"`, `"VF_SIP=disabled"`} {
		if !strings.Contains(compact(string(uitest)), compact(want)) {
			t.Fatalf("uitest recipe lacks %s", want)
		}
	}
	if strings.Contains(string(uitest), "recovery_partition") {
		t.Fatal("the plugin cannot relocate the macOS 27 recovery partition")
	}
	if _, err := recipe.ReadFile("recipe/disable-sip.pkr.hcl"); err == nil {
		t.Fatal("Recovery SIP stage is not used: the pinned upstream ships SIP disabled")
	}
	dir := filepath.Join(t.TempDir(), "recipe")
	if err := writeRecipe(dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "scripts", "provision27.sh")); err != nil || string(b) != guestscripts.Provision27 {
		t.Fatal("shared UI-test recipe not materialized", err)
	}
	if out, err := exec.Command("/bin/bash", "-n", filepath.Join(dir, "scripts", "adopt-xcode.sh")).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
}
