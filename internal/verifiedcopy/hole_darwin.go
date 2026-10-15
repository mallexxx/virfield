package verifiedcopy

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fpunchhole matches fpunchhole_t in Darwin's <sys/fcntl.h>. F_PUNCHHOLE
// requires filesystem-block-aligned ranges and never changes the file size.
type fpunchhole struct {
	flags    uint32
	reserved uint32
	offset   int64
	length   int64
}

func punchHole(file *os.File, offset, length int64) error {
	request := &fpunchhole{offset: offset, length: length}
	// FcntlInt uses the supported libSystem wrapper. Its integer argument
	// carries a pointer for this command, so pin the request across the call.
	var pinned runtime.Pinner
	pinned.Pin(request)
	defer pinned.Unpin()
	_, err := unix.FcntlInt(file.Fd(), unix.F_PUNCHHOLE, int(uintptr(unsafe.Pointer(request))))
	runtime.KeepAlive(file)
	return err
}
