package tartimage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/guestscripts"
)

func TestRecipeDigestIsStableAndPinned(t *testing.T) {
	if RecipeDigest() != RecipeDigest() || len(RecipeDigest()) != 64 {
		t.Fatal("unstable recipe digest")
	}
	template, err := recipe.ReadFile("recipe/developer.pkr.hcl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`version = "= 1.21.0"`, `recovery_partition = "relocate"`, "disk_size_gb       = 120", `script = "scripts/reset-host-keys.sh"`, `script = "scripts/grow-disk.sh"`} {
		if !strings.Contains(string(template), want) {
			t.Fatalf("recipe lacks %s", want)
		}
	}
}

func TestRecipeScriptsParseAndMaterializePrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recipe")
	if err := writeRecipe(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check-guest.sh", "install-xcode.sh", "xcode-post.sh", "reset-host-keys.sh", "grow-disk.sh"} {
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

func TestUITestRecipeDisablesSIPInRecoveryAndProvisionsInPlace(t *testing.T) {
	sip, err := recipe.ReadFile("recipe/disable-sip.pkr.hcl")
	if err != nil {
		t.Fatal(err)
	}
	uitest, err := recipe.ReadFile("recipe/uitest.pkr.hcl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`version = "= 1.21.0"`, "recovery     = true", `"<wait10s>csrutil disable<enter>"`} {
		if !strings.Contains(string(sip), want) {
			t.Fatalf("disable-sip recipe lacks %s", want)
		}
	}
	if strings.Contains(string(uitest), "vm_base_name") || !strings.Contains(string(uitest), `script           = "scripts/provision27.sh"`) {
		t.Fatal("uitest recipe must provision the cloned VM in place with the shared recipe")
	}
	dir := filepath.Join(t.TempDir(), "recipe")
	if err := writeRecipe(dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "scripts", "provision27.sh")); err != nil || string(b) != guestscripts.Provision27 {
		t.Fatal("shared UI-test recipe not materialized", err)
	}
}
