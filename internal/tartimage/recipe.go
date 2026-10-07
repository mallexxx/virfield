// Package tartimage builds and qualifies Tart goldens from pinned Cirrus
// images with Packer and Virfield's guest qualification.
package tartimage

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed recipe
var recipe embed.FS

// RecipeDigest identifies the exact embedded recipe for image evidence.
func RecipeDigest() string {
	h := sha256.New()
	_ = fs.WalkDir(recipe, "recipe", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := recipe.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(b))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// writeRecipe materializes the recipe into a fresh private directory.
func writeRecipe(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return fs.WalkDir(recipe, "recipe", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, strings.TrimPrefix(path, "recipe"))
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		b, err := recipe.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0600)
	})
}
