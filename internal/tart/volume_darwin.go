package tart

import (
	"errors"
	"os"
	"syscall"
)

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
	mount := volumeMount{fsid: fs.Fsid.Val, point: text(fs.Mntonname[:]), device: text(fs.Mntfromname[:])}
	if mount.point == "" || mount.point[0] != '/' || mount.device == "" || fs.Bsize == 0 {
		return mountedVolume{}, errors.New("invalid volume mount information")
	}
	maxBlocks := uint64(1<<63-1) / uint64(fs.Bsize)
	if fs.Blocks > maxBlocks || fs.Bavail > maxBlocks {
		return mountedVolume{}, errors.New("volume size exceeds supported range")
	}
	return mountedVolume{mount: mount, total: int64(fs.Blocks * uint64(fs.Bsize)), free: int64(fs.Bavail * uint64(fs.Bsize))}, nil
}

// mountPoint returns the mount point of the volume that contains path.
func mountPoint(path string) (string, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return "", err
	}
	name := make([]byte, 0, len(fs.Mntonname))
	for _, c := range fs.Mntonname {
		if c == 0 {
			break
		}
		name = append(name, byte(c))
	}
	if len(name) == 0 || name[0] != '/' {
		return "", errors.New("volume has no mount point")
	}
	return string(name), nil
}
