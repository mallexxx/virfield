// Package hostlock enforces one Virfield control plane per host, including
// daemons configured with different databases or listening ports.
package hostlock

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func Acquire() (*os.File, error) {
	return acquireAt(fmt.Sprintf("/tmp/virfield-control-%d", os.Getuid()))
}
func AcquireHost() (*os.File, error) { return acquireAt("/tmp/virfield-control-host") }
func acquireAt(dir string) (*os.File, error) {
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 || !ok || int(stat.Uid) != os.Getuid() {
		return nil, fmt.Errorf("host lock directory must be private and owned by this Virfield service user")
	}
	f, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another virfieldd owns this host: %w", err)
	}
	return f, nil // Closing the file releases the kernel lock, including after a crash.
}
