package tart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/verifiedcopy"
)

// CopyImage holds the same config lock as tart run until every file and both
// configured storage identities are verified. It never changes UUID/MAC/NVRAM.
func (c *Client) CopyImage(ctx context.Context, source, destination domain.Lease) (string, error) {
	if !domain.ValidName(source.VMName) || !domain.ValidName(destination.VMName) {
		return "", domain.Err("invalid_request", "Runtime copy requires safe VM names")
	}
	src, err := c.container(source.Location)
	if err != nil {
		return "", err
	}
	dst, err := c.container(destination.Location)
	if err != nil {
		return "", err
	}
	check := func(v ContainerConfig) (string, error) {
		info, err := c.probe(ctx, v.Path)
		if err != nil {
			return "", err
		}
		if info.Identity == "" || v.VolumeUUID != "" && !strings.EqualFold(v.VolumeUUID, info.Identity) {
			return "", domain.Err("resource_unavailable", "Storage identity changed during local transfer")
		}
		return info.Identity, nil
	}
	srcID, err := check(src)
	if err != nil {
		return "", err
	}
	dstID, err := check(dst)
	if err != nil {
		return "", err
	}
	sourceDir := filepath.Join(src.Path, "vms", source.VMName)
	destDir := filepath.Join(dst.Path, "vms", destination.VMName)
	if sourceDir == destDir {
		return "", domain.Err("invalid_request", "Source and destination must differ")
	}
	if err := os.Mkdir(filepath.Dir(destDir), 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	for _, p := range []string{sourceDir, filepath.Dir(destDir)} {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", domain.Err("image_transfer_failed", "Runtime directory is missing or unsafe")
		}
	}
	configPath := filepath.Join(sourceDir, "config.json")
	c.specMu.Lock()
	if c.transfers[configPath] {
		c.specMu.Unlock()
		return "", domain.Err("image_in_use", "Source already has a local transfer in flight")
	}
	config, err := os.OpenFile(configPath, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err == nil {
		lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
		err = syscall.FcntlFlock(config.Fd(), syscall.F_SETLK, &lock)
	}
	if err != nil {
		if config != nil {
			config.Close()
		}
		c.specMu.Unlock()
		return "", domain.Err("image_in_use", "Source runtime config could not be locked; transfer did not start")
	}
	if c.transfers == nil {
		c.transfers = map[string]bool{}
	}
	c.transfers[configPath] = true
	c.specMu.Unlock()
	defer func() { c.specMu.Lock(); config.Close(); delete(c.transfers, configPath); c.specMu.Unlock() }()
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return "", err
	}
	files := map[string]string{}
	sourceInfo := map[string]os.FileInfo{}
	for _, entry := range entries {
		if entry.Name() == ".virfield-transfer-owner" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSocket != 0 && entry.Name() == "control.sock" {
			continue
		}
		if !info.Mode().IsRegular() {
			return "", domain.Err("image_transfer_unsupported", "Local transfer requires a standalone VM without symlinks, saved state or disk stacks")
		}
		if entry.Name() != "config.json" && entry.Name() != "disk.img" && entry.Name() != "nvram.bin" {
			return "", domain.Err("image_transfer_unsupported", "Standalone VM contains unrecognized runtime files; saved state and disk stacks cannot be transferred")
		}
		files[entry.Name()] = ""
		sourceInfo[entry.Name()] = info
	}
	for _, name := range []string{"config.json", "disk.img", "nvram.bin"} {
		if _, ok := files[name]; !ok {
			return "", domain.Err("image_transfer_unsupported", "Standalone Tart VM config, disk and NVRAM are required")
		}
	}
	if err := os.Mkdir(destDir, 0700); err != nil {
		return "", domain.Err("image_exists", "Destination exists or cannot be reserved; nothing overwritten")
	}
	if err := os.WriteFile(filepath.Join(destDir, ".virfield-transfer-owner"), []byte(destination.ID), 0600); err != nil {
		return "", err
	}
	// Publish config last so partial data never resembles a complete runtime VM.
	names := make([]string, 0, len(files))
	for name := range files {
		if name != "config.json" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names = append(names, "config.json")
	for _, name := range names {
		file := config
		if name != "config.json" {
			file, err = verifiedcopy.Open(filepath.Join(sourceDir, name))
			if err != nil {
				return "", err
			}
		}
		digest, copyErr := verifiedcopy.File(ctx, file, filepath.Join(destDir, name))
		if name != "config.json" {
			file.Close()
		}
		if copyErr != nil {
			return "", copyErr
		}
		files[name] = digest
	}
	for name, before := range sourceInfo {
		after, err := os.Lstat(filepath.Join(sourceDir, name))
		if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return "", domain.Err("image_transfer_mismatch", "Source runtime identity changed during transfer")
		}
	}
	if id, e := check(src); e != nil || id != srcID {
		return "", domain.Err("resource_unavailable", "Source storage changed during transfer")
	}
	if id, e := check(dst); e != nil || id != dstID {
		return "", domain.Err("resource_unavailable", "Destination storage changed during transfer")
	}
	if err := verifiedcopy.SyncDirectory(destDir); err != nil {
		return "", err
	}
	if err := verifiedcopy.SyncDirectory(filepath.Dir(destDir)); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(files)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// DiscardImageCopy requires an explicitly confirmed image recovery job. Missing
// config.json alone does not prove that partial disk data is absent.
func (c *Client) DiscardImageCopy(ctx context.Context, l domain.Lease) error {
	if !domain.ValidName(l.ID) || !domain.ValidName(l.VMName) || l.Purpose != "image" || l.Source == nil || l.Source.Location == l.Location && l.Source.Name == l.VMName {
		return domain.Err("ownership_unconfirmed", "Not an owned transfer destination")
	}
	container, err := c.container(l.Location)
	if err != nil {
		return err
	}
	dir := filepath.Join(container.Path, "vms", l.VMName)
	st, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return domain.Err("ownership_unconfirmed", "Transfer destination is unsafe")
	}
	owner, err := os.ReadFile(filepath.Join(dir, ".virfield-transfer-owner"))
	if err != nil || string(owner) != l.ID {
		return domain.Err("ownership_unconfirmed", "Partial copy has no matching ownership marker; inspect without deletion")
	}
	if _, err := os.Lstat(filepath.Join(dir, "config.json")); err == nil {
		if locked, err := runLocked(filepath.Join(dir, "config.json")); err != nil || locked {
			return domain.Err("image_in_use", "Transfer destination is locked or cannot be inspected")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return domain.Err("ownership_unconfirmed", "Partial copy contains unexpected entries; inspect before deleting")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return verifiedcopy.SyncDirectory(filepath.Dir(dir))
}
