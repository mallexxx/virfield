package hostresources

import "golang.org/x/sys/unix"

func Memory() (int64, error) {
	var s unix.Sysinfo_t
	e := unix.Sysinfo(&s)
	return int64(s.Totalram) * int64(s.Unit), e
}
