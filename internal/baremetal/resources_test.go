package baremetal

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestNodeVMStorageRootRoutesWorkspacesAndBlocksChangingActiveRoots(t *testing.T) {
	r, local, _ := vmRuntime(t, "secured")
	root := filepath.Join(t.TempDir(), "runtime")
	r.nodes[0].VMs.StorageDir = root
	persist := t.TempDir()
	if err := r.Persist(persist); err != nil {
		t.Fatal(err)
	}
	o, err := r.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o.StorageContainers[0].Path != root {
		t.Fatal("Storage inventory lost configured root", o.StorageContainers)
	}
	l := domain.Lease{ID: "lease-storage-test", VMName: "vf-storage-test", Location: "linux-node", ImageID: "image-ready"}
	if err := r.Clone(context.Background(), domain.Template{Name: "secured"}, l); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "leases", l.VMName))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("VM workspace not private in configured root", err)
	}
	if _, err := os.Stat(filepath.Join(local.homes["linux-node"], "virfield/leases", l.VMName)); !os.IsNotExist(err) {
		t.Fatal("VM workspace created in old home", err)
	}
	if _, err := r.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := r.nodes[0]
	copied := *other.VMs
	copied.StorageDir = t.TempDir()
	other.VMs = &copied
	restarted := New([]Node{other}, local.dial)
	if err := restarted.Persist(persist); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Fatal("Root change hid existing overlay", err)
	}
	if err := r.Delete(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "leases", l.VMName)); !os.IsNotExist(err) {
		t.Fatal("VM cleanup used wrong root", err)
	}
}

func writeQCOWHeader(t *testing.T, path string, size uint64) {
	t.Helper()
	header := make([]byte, 72)
	copy(header, []byte{'Q', 'F', 'I', 0xfb})
	binary.BigEndian.PutUint32(header[4:8], 3)
	binary.BigEndian.PutUint64(header[24:32], size)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, header, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
}

func TestFreshNodeQuotaCheckRefusesChangedSourceAndCacheBeforeClone(t *testing.T) {
	r, local, goldens := vmRuntime(t, "secured")
	writeQCOWHeader(t, filepath.Join(goldens, "secured.qcow2"), 200<<30)
	l := domain.Lease{ID: "lease-0123456789abcdef", VMName: "vf-0123456789abcdef", Location: "linux-node", ImageID: "image-0123", Resources: domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100 << 30}}
	err := r.Clone(context.Background(), domain.Template{Name: "secured"}, l)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "resource_unknown" {
		t.Fatal("Changed source admitted", err)
	}
	if _, err := os.Stat(filepath.Join(local.homes["linux-node"], "virfield/leases", l.VMName)); !os.IsNotExist(err) {
		t.Fatal("Refused clone touched node workspace", err)
	}
	o, err := r.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	vm, ok := vmNamed(o, "secured")
	if !ok || vm.Resources.DiskBytes != 200<<30 {
		t.Fatal("QCOW2 virtual size not observed", vm)
	}
	if o.StorageContainers[0].AvailableBytes < 1 || o.StorageContainers[0].CacheBytes < 1 {
		t.Fatal("Node storage not observed", o.StorageContainers)
	}
	if len(o.StorageContainers[0].Disks) != 2 || !o.StorageContainers[0].Disks[0].Observed || !o.StorageContainers[0].Disks[1].Observed {
		t.Fatal("Per-filesystem df observations missing", o.StorageContainers)
	}
	l.Resources.DiskBytes = 200 << 30
	l.VMName = "test-image"
	l.Purpose = "image"
	r.nodes[0].VMs.CacheLimitBytes = 150 << 30
	err = r.CloneImage(context.Background(), l, "secured")
	if !errors.As(err, &failure) || failure.Code != "resource_exhausted" {
		t.Fatal("Cache quota bypassed", err)
	}
	if failure.Admission == nil || !strings.Contains(failure.Message, "image_cache") || !strings.Contains(failure.Message, "214748364800 bytes") || !strings.Contains(failure.Message, "GiB") {
		t.Fatal("Fresh runtime refusal lost quota arithmetic", failure)
	}
	if _, err := os.Stat(filepath.Join(local.homes["linux-node"], "virfield/images", l.VMName)); !os.IsNotExist(err) {
		t.Fatal("Refused build touched node workspace", err)
	}
}
