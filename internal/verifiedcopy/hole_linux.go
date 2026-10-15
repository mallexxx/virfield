package verifiedcopy

import (
	"os"

	"golang.org/x/sys/unix"
)

func punchHole(file *os.File, offset, length int64) error {
	return unix.Fallocate(int(file.Fd()), unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, offset, length)
}
