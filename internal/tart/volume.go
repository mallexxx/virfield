package tart

import (
	"context"
	"errors"
	"sync"
)

// UUID lookup uses Disk Arbitration through diskutil, which can take tens of
// seconds on a busy Mac. Cache only that identity, bound to the kernel's mount
// identity. Directory availability and allocatable bytes are always read anew.
// XNU assigns a unique fsid to a mount (vfs_getnewfsid); the device and mount
// point also participate so an unmounted/replaced volume cannot reuse a hit.
type volumeMount struct {
	fsid   [2]int32
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
