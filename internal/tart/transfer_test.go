package tart

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestLocalImageCopyPreservesIdentityAndLockAcrossInventory(t *testing.T) {
	src, dst := tartHome(t, "golden"), tartHome(t)
	dir := filepath.Join(src, "vms", "golden")
	for name, data := range map[string]string{"config.json": `{"uuid":"preserve-me","macAddress":"aa:bb:cc:dd:ee:ff"}`, "disk.img": "encrypted disk identity", "nvram.bin": "original nvram"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := &Client{config: Config{Containers: []ContainerConfig{{ID: "source", Path: src}, {ID: "destination", Path: dst}}}}
	checks := 0
	c.probe = func(_ context.Context, path string) (volume, error) {
		checks++
		if checks == 3 {
			// A same-process close here used to release Tart's POSIX lock.
			if _, err := c.list(context.Background(), ContainerConfig{Path: src}); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/usr/bin/python3", "-c", `import fcntl,sys
f=open(sys.argv[1],'r+')
try: fcntl.lockf(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError: sys.exit(0)
sys.exit(1)`, filepath.Join(dir, "config.json"))
			if err := cmd.Run(); err != nil {
				t.Fatal("source lock lost during inventory", err)
			}
		}
		return volume{Identity: path, Free: 1 << 40}, nil
	}
	source := domain.Lease{ID: "image-source", VMName: "golden", Location: "source"}
	destination := domain.Lease{ID: "image-copy", VMName: "copied", Location: "destination"}
	digest, err := c.CopyImage(context.Background(), source, destination)
	if err != nil || len(digest) != 64 || checks != 4 {
		t.Fatal(digest, checks, err)
	}
	for _, name := range []string{"config.json", "disk.img", "nvram.bin"} {
		a, _ := os.ReadFile(filepath.Join(dir, name))
		b, err := os.ReadFile(filepath.Join(dst, "vms", "copied", name))
		if err != nil || string(a) != string(b) {
			t.Fatal("identity changed", name, err)
		}
	}
	if locked, err := runLocked(filepath.Join(dir, "config.json")); err != nil || locked {
		t.Fatal("lock leaked", locked, err)
	}
	if _, err := c.CopyImage(context.Background(), source, destination); err == nil {
		t.Fatal("overwrote destination")
	}
}

func TestLocalImageCopyRefusesSymlinksAndChangedStorage(t *testing.T) {
	for _, scenario := range []string{"symlink", "volume"} {
		t.Run(scenario, func(t *testing.T) {
			src, dst := tartHome(t, "golden"), tartHome(t)
			dir := filepath.Join(src, "vms", "golden")
			os.WriteFile(filepath.Join(dir, "disk.img"), []byte("disk"), 0600)
			os.WriteFile(filepath.Join(dir, "nvram.bin"), []byte("nvram"), 0600)
			if scenario == "symlink" {
				os.Symlink("disk.img", filepath.Join(dir, "stack"))
			}
			calls := 0
			c := &Client{config: Config{Containers: []ContainerConfig{{ID: "source", Path: src}, {ID: "destination", Path: dst}}}}
			c.probe = func(context.Context, string) (volume, error) {
				calls++
				id := "original"
				if calls > 2 {
					id = "replacement"
				}
				return volume{Identity: id, Free: 1 << 40}, nil
			}
			digest, err := c.CopyImage(context.Background(), domain.Lease{VMName: "golden", Location: "source"}, domain.Lease{VMName: "copied", Location: "destination"})
			if err == nil || digest != "" {
				t.Fatal("unsafe copy verified", scenario, digest, err)
			}
			if scenario == "symlink" && !strings.Contains(err.Error(), "standalone") {
				t.Fatal(err)
			}
		})
	}
}

func TestPartialImageCopyCleanupRequiresExactMarkerWithoutConfig(t *testing.T) {
	home := tartHome(t, "partial")
	dir := filepath.Join(home, "vms", "partial")
	os.Remove(filepath.Join(dir, "config.json"))
	os.WriteFile(filepath.Join(dir, "disk.img"), []byte("partial disk"), 0600)
	c := &Client{config: Config{Containers: []ContainerConfig{{ID: "destination", Path: home}}}}
	l := domain.Lease{ID: "image-partial", Purpose: "image", Location: "destination", VMName: "partial", Source: &domain.Template{Location: "source", Name: "original"}}
	if err := c.DiscardImageCopy(context.Background(), l); err == nil {
		t.Fatal("deleted unmarked foreign partial")
	}
	os.WriteFile(filepath.Join(dir, ".virfield-transfer-owner"), []byte("image-foreign"), 0600)
	if err := c.DiscardImageCopy(context.Background(), l); err == nil {
		t.Fatal("deleted another image's partial")
	}
	os.WriteFile(filepath.Join(dir, ".virfield-transfer-owner"), []byte(l.ID), 0600)
	if err := c.DiscardImageCopy(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("partial data retained after confirmed cleanup", err)
	}
}
