package tart

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
)

// UUID lookup uses Disk Arbitration through diskutil, which can take tens of
// seconds on a busy Mac. Cache only that identity, bound to the kernel's mount
// identity. Directory availability and allocatable bytes are always read anew.
// XNU assigns a unique fsid to a mount (vfs_getnewfsid); the device and mount
// point also participate so an unmounted/replaced volume cannot reuse a hit.
type volumeMount struct {
	fsid   syscall.Fsid
	point  string
	device string
}

type mountedVolume struct {
	mount volumeMount
	total int64
	free  int64
}

type volumeIdentity struct {
	mount volumeMount
	uuid  string
}

type volumeProbe struct {
	stat   func(string) (mountedVolume, error)
	lookup func(context.Context, string) (volume, error)
	mu     sync.Mutex
	cache  map[string]volumeIdentity
}

var errVolumeChanged = errors.New("volume changed during identity lookup")

func (p *volumeProbe) read(ctx context.Context, path string) (volume, error) {
	if err := ctx.Err(); err != nil {
		return volume{}, err
	}
	fresh, err := p.stat(path)
	if err != nil {
		return volume{}, err
	}
	p.mu.Lock()
	identity, ok := p.cache[fresh.mount.point]
	p.mu.Unlock()
	if !ok || identity.mount != fresh.mount {
		verified, err := p.lookup(ctx, path)
		if err != nil {
			return volume{}, err
		}
		if verified.Identity == "" {
			return volume{}, errors.New("volume has no stable UUID")
		}
		after, err := p.stat(path)
		if err != nil {
			return volume{}, err
		}
		if after.mount != fresh.mount {
			return volume{}, errVolumeChanged
		}
		fresh = after
		identity = volumeIdentity{mount: fresh.mount, uuid: verified.Identity}
		p.mu.Lock()
		if p.cache == nil {
			p.cache = map[string]volumeIdentity{}
		}
		p.cache[fresh.mount.point] = identity
		p.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return volume{}, err
	}
	return volume{Identity: identity.uuid, Total: fresh.total, Free: fresh.free}, nil
}

func statVolume(path string) (mountedVolume, error) {
	info, err := os.Stat(path)
	if err != nil {
		return mountedVolume{}, err
	}
	if !info.IsDir() {
		return mountedVolume{}, errors.New("tart storage path is not a directory")
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return mountedVolume{}, err
	}
	text := func(buf []int8) string {
		value := make([]byte, 0, len(buf))
		for _, c := range buf {
			if c == 0 {
				break
			}
			value = append(value, byte(c))
		}
		return string(value)
	}
	mount := volumeMount{fsid: fs.Fsid, point: text(fs.Mntonname[:]), device: text(fs.Mntfromname[:])}
	if mount.point == "" || mount.point[0] != '/' || mount.device == "" || fs.Bsize == 0 {
		return mountedVolume{}, errors.New("invalid volume mount information")
	}
	maxBlocks := uint64(1<<63-1) / uint64(fs.Bsize)
	if fs.Blocks > maxBlocks || fs.Bavail > maxBlocks {
		return mountedVolume{}, errors.New("volume size exceeds supported range")
	}
	return mountedVolume{mount: mount, total: int64(fs.Blocks * uint64(fs.Bsize)), free: int64(fs.Bavail * uint64(fs.Bsize))}, nil
}
