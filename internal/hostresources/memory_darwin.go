package hostresources

import "golang.org/x/sys/unix"

func Memory() (int64, error) { v, e := unix.SysctlUint64("hw.memsize"); return int64(v), e }
