package hostresources

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func Disk(paths map[string]string) (map[string]int64, error) {
	out := map[string]int64{}
	for name, path := range paths {
		var s unix.Statfs_t
		if err := unix.Statfs(path, &s); err != nil {
			return nil, fmt.Errorf("storage %s unavailable", name)
		}
		out[name] = int64(s.Bavail) * int64(s.Bsize)
	}
	return out, nil
}
