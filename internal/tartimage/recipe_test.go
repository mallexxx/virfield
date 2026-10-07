package tartimage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeDigestIsStableAndPinned(t *testing.T) {
	if RecipeDigest() != RecipeDigest() || len(RecipeDigest()) != 64 {
		t.Fatal("unstable recipe digest")
	}
	template, err := recipe.ReadFile("recipe/developer.pkr.hcl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`version = "= 1.21.0"`, `recovery_partition = "relocate"`, "disk_size_gb       = 120"} {
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
	for _, name := range []string{"check-guest.sh", "install-xcode.sh", "xcode-post.sh"} {
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
