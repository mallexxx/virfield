package hostresources

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

func Disk(paths map[string]string) (map[string]int64, error) {
	out := map[string]int64{}
	unavailable := []string{}
	for name, path := range paths {
		var s unix.Statfs_t
		if err := unix.Statfs(path, &s); err != nil {
			unavailable = append(unavailable, name)
			continue
		}
		out[name] = int64(s.Bavail) * int64(s.Bsize)
	}
	if len(out) == 0 && len(unavailable) > 0 {
		sort.Strings(unavailable)
		return nil, fmt.Errorf("all configured storage locations are unavailable: %s", strings.Join(unavailable, ", "))
	}
	return out, nil
}
