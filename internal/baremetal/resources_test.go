package baremetal

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

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
	l := domain.Lease{ID: "lease-0123456789abcdef", VMName: "vf-0123456789abcdef", Location: "htpc", ImageID: "image-0123", Resources: domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100 << 30}}
	err := r.Clone(context.Background(), domain.Template{Name: "secured"}, l)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "resource_unknown" {
		t.Fatal("Changed source admitted", err)
	}
	if _, err := os.Stat(filepath.Join(local.homes["htpc"], "virfield/leases", l.VMName)); !os.IsNotExist(err) {
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
	l.Resources.DiskBytes = 200 << 30
	l.VMName = "test-image"
	l.Purpose = "image"
	r.nodes[0].VMs.CacheLimitBytes = 150 << 30
	err = r.CloneImage(context.Background(), l, "secured")
	if !errors.As(err, &failure) || failure.Code != "resource_exhausted" {
		t.Fatal("Cache quota bypassed", err)
	}
	if _, err := os.Stat(filepath.Join(local.homes["htpc"], "virfield/images", l.VMName)); !os.IsNotExist(err) {
		t.Fatal("Refused build touched node workspace", err)
	}
}
