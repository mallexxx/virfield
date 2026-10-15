package verifiedcopy

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// preserveHoles removes destination blocks covering source holes. Skipping
// writes alone is insufficient: APFS can allocate gaps surrounding small writes,
// and an I/O buffer containing any data can span many smaller source holes.
// Run after all writes/truncation, before hashing either file again. Only the
// destination is changed; the subsequent full hashes also verify punched ranges.
func preserveHoles(ctx context.Context, source, destination *os.File, size int64) error {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(destination.Fd()), &fs); err != nil {
		return err
	}
	block := int64(fs.Bsize)
	if block <= 0 {
		return errors.New("unknown destination filesystem block size")
	}
	position, err := source.Seek(0, 1)
	if err != nil {
		return err
	}
	defer source.Seek(position, 0)
	for offset := int64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		hole, err := unix.Seek(int(source.Fd()), offset, unix.SEEK_HOLE)
		if err != nil {
			return fmt.Errorf("observe source sparse ranges: %w", err)
		}
		if hole < offset || hole > size {
			return errors.New("invalid source hole offset")
		}
		if hole == size {
			return nil
		}
		data, err := unix.Seek(int(source.Fd()), hole, unix.SEEK_DATA)
		if errors.Is(err, unix.ENXIO) {
			data, err = size, nil
		}
		if err != nil {
			return fmt.Errorf("observe source data ranges: %w", err)
		}
		if data <= hole || data > size {
			return errors.New("invalid source data offset")
		}
		// Filesystems may have different block sizes. Leave partial boundary
		// blocks intact; the content verification still covers those bytes.
		start, end := hole, data-data%block
		if remainder := start % block; remainder != 0 {
			if block-remainder > size-start {
				return nil
			}
			start += block - remainder
		}
		if start < end {
			if err := punchHole(destination, start, end-start); err != nil {
				return fmt.Errorf("preserve destination sparse range: %w", err)
			}
		}
		offset = data
	}
	return nil
}
