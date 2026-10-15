package tart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVolumeRefreshReadsFreeSpaceAndRejectsReplacedOrMissingMount(t *testing.T) {
	mounted := mountedVolume{mount: volumeMount{point: "/external", device: "/dev/disk5s1"}, total: 1000, free: 900}
	mounted.mount.fsid.Val[0] = 1
	var unavailable error
	lookups := 0
	p := &volumeProbe{
		stat: func(string) (mountedVolume, error) { return mounted, unavailable },
		lookup: func(context.Context, string) (volume, error) {
			lookups++
			id := "original"
			if mounted.mount.fsid.Val[0] == 2 {
				id = "replacement"
			}
			return volume{Identity: id, Total: 1, Free: 1}, nil
		},
	}
	ctx := context.Background()
	first, err := p.read(ctx, "/external/vms")
	if err != nil || first.Identity != "original" || first.Total != 1000 || first.Free != 900 {
		t.Fatal(first, err)
	}
	mounted.free = 100 // A VM's disk grew without any mount or UUID change.
	next, err := p.read(ctx, "/external/vms")
	if err != nil || next.Free != 100 || lookups != 1 {
		t.Fatal("free space was cached, or diskutil ran again", next, lookups, err)
	}
	unavailable = os.ErrNotExist
	if _, err := p.read(ctx, "/external/vms"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing storage reused its cached identity", err)
	}
	unavailable = nil
	mounted.mount.fsid.Val[0] = 2 // Another volume mounted at the same path/device.
	replaced, err := p.read(ctx, "/external/vms")
	if err != nil || replaced.Identity != "replacement" || lookups != 2 {
		t.Fatal("replacement volume reused the original UUID", replaced, lookups, err)
	}
}

func TestVolumeRefreshCannotCacheIdentityAcrossMountChangeOrFailedLookup(t *testing.T) {
	mounted := mountedVolume{mount: volumeMount{point: "/external", device: "/dev/disk5s1"}}
	lookupErr := errors.New("diskutil unavailable")
	lookups := 0
	p := &volumeProbe{
		stat: func(string) (mountedVolume, error) { return mounted, nil },
		lookup: func(context.Context, string) (volume, error) {
			lookups++
			mounted.mount.fsid.Val[0]++ // Unmount/remount while diskutil was running.
			return volume{Identity: "old-volume"}, lookupErr
		},
	}
	if _, err := p.read(context.Background(), "/external/vms"); !errors.Is(err, lookupErr) {
		t.Fatal(err)
	}
	lookupErr = nil
	if _, err := p.read(context.Background(), "/external/vms"); !errors.Is(err, errVolumeChanged) {
		t.Fatal("identity lookup raced a replacement volume", err)
	}
	if len(p.cache) != 0 || lookups != 2 {
		t.Fatal("unverified identity was cached", p.cache, lookups)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.read(ctx, "/external/vms"); !errors.Is(err, context.Canceled) || lookups != 2 {
		t.Fatal("canceled refresh performed I/O", err, lookups)
	}
}

func TestStatVolumeUsesActualDirectoryAndAllocatableBytes(t *testing.T) {
	dir := t.TempDir()
	v, err := statVolume(dir)
	if err != nil || v.mount.point == "" || v.mount.point == dir || v.total <= 0 || v.free < 0 || v.free > v.total {
		t.Fatal(v, err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := statVolume(file); err == nil {
		t.Fatal("file accepted as storage directory")
	}
	if _, err := statVolume(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("absent directory accepted", err)
	}
}

func TestLiveMountedVolumeIdentity(t *testing.T) {
	paths := os.Getenv("VIRFIELD_LIVE_VOLUME_PATHS")
	if paths == "" {
		t.Skip("requires explicitly selected mounted storage directories")
	}
	for _, path := range filepath.SplitList(paths) {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			r := &execRunner{}
			calls := 0
			p := &volumeProbe{stat: statVolume, lookup: func(ctx context.Context, path string) (volume, error) {
				calls++
				return r.probeVolume(ctx, path)
			}}
			first, err := p.read(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			for range 20 {
				v, err := p.read(ctx, path)
				if err != nil || v.Identity != first.Identity || v.Total <= 0 || v.Free < 0 || v.Free > v.Total {
					t.Fatal(v, err)
				}
			}
			if calls != 1 {
				t.Fatal("unchanged mount repeatedly queried Disk Arbitration", calls)
			}
			t.Logf("UUID %s; twenty fresh capacity probes took %s, one diskutil lookup", first.Identity, time.Since(started))
		})
	}
}
