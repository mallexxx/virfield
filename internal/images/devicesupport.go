package images

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

const dscExtractorSource = `#include <dlfcn.h>
#include <stdio.h>
typedef int (*extract_fn)(const char *, const char *, void (^)(unsigned, unsigned));
int main(int argc, char **argv) {
  if (argc != 4) return 64;
  void *handle = dlopen(argv[1], RTLD_NOW);
  if (!handle) { fprintf(stderr, "dlopen: %s\n", dlerror()); return 65; }
  extract_fn extract = (extract_fn)dlsym(handle, "dyld_shared_cache_extract_dylibs_progress");
  if (!extract) { fprintf(stderr, "dlsym: %s\n", dlerror()); return 66; }
  return extract(argv[2], argv[3], ^(unsigned current, unsigned total) {
    if (current == total || current % 250 == 0) fprintf(stderr, "%u/%u\n", current, total);
  });
}
`

func deviceSupportPath(home, version, build string) (string, error) {
	if !filepath.IsAbs(home) || !domain.ValidVersion(version) || !domain.ValidName(build) {
		return "", domain.Err("invalid_profile", "DeviceSupport needs a valid host home and macOS version/build")
	}
	return filepath.Join(home, "Library", "Developer", "Xcode", "macOS DeviceSupport", version+" ("+build+")", "Symbols"), nil
}

func (e *Engine) prepareDeviceSupport(ctx context.Context, guestIP, version, build string, progress func(string) error) (string, error) {
	if net.ParseIP(guestIP) == nil {
		return "", domain.Err("device_support_failed", "Golden VM has no valid IP for its read-only dyld export")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	target, err := deviceSupportPath(home, version, build)
	if err != nil {
		return "", err
	}
	probe := filepath.Join(target, "usr", "lib", "libobjc.A.dylib")
	if st, statErr := os.Lstat(probe); statErr == nil && st.Mode().IsRegular() {
		return target, nil
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err := progress("Preparing Xcode DeviceSupport symbols from the golden VM dyld cache"); err != nil {
		return "", err
	}
	developerOut, err := exec.CommandContext(ctx, "/usr/bin/xcode-select", "-p").Output()
	if err != nil {
		return "", domain.Err("device_support_failed", "Host Xcode developer directory is unavailable")
	}
	developer := strings.TrimSpace(string(developerOut))
	bundle := filepath.Join(developer, "Platforms", "iPhoneOS.platform", "usr", "lib", "dsc_extractor.bundle")
	if st, statErr := os.Lstat(bundle); statErr != nil || !st.IsDir() {
		return "", domain.Err("device_support_failed", "Host Xcode has no dyld shared-cache extractor")
	}
	cacheDir := filepath.Join(e.Dir, "cache", "device-support")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", err
	}
	source := filepath.Join(cacheDir, "dsc-extractor.c")
	extractor := filepath.Join(cacheDir, "dsc-extractor")
	if err := os.WriteFile(source, []byte(dscExtractorSource), 0600); err != nil {
		return "", err
	}
	compile := exec.CommandContext(ctx, "/usr/bin/xcrun", "clang", "-fblocks", source, "-o", extractor, "-ldl")
	if output, compileErr := compile.CombinedOutput(); compileErr != nil {
		_ = os.WriteFile(filepath.Join(cacheDir, "compile.log"), output, 0600)
		return "", domain.Err("device_support_failed", "Host dyld shared-cache extractor could not be built")
	}
	mountpoint, err := os.MkdirTemp("/private/tmp", "virfield-dyld-"+build+"-")
	if err != nil {
		return "", err
	}
	defer os.Remove(mountpoint)
	mounted := false
	shares := []string{
		guestIP + ":/System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld",
		guestIP + ":/System/Library/dyld",
	}
	for _, share := range shares {
		for attempt := 0; attempt < 3; attempt++ {
			mountCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			mountErr := exec.CommandContext(mountCtx, "/sbin/mount_nfs", "-o", "ro,nolocks", share, mountpoint).Run()
			cancel()
			if mountErr == nil {
				mounted = true
				break
			}
			if err := pause(ctx, time.Duration(attempt+1)*time.Second); err != nil {
				return "", err
			}
		}
		if mounted {
			break
		}
	}
	if !mounted {
		return "", domain.Err("device_support_failed", "Golden VM dyld NFS export could not be mounted read-only")
	}
	defer func() {
		unmountCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(unmountCtx, "/sbin/umount", mountpoint).Run()
	}()
	cache := filepath.Join(mountpoint, "dyld_shared_cache_arm64e")
	if st, statErr := os.Lstat(cache); statErr != nil || !st.Mode().IsRegular() {
		return "", domain.Err("device_support_failed", "Golden VM dyld shared cache is missing")
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, ".Symbols-"+build+"-")
	if err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(staging)
		}
	}()
	logPath := filepath.Join(e.Dir, "images", "device-support-"+build+".log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, extractor, bundle, cache, staging)
	command.Stdout, command.Stderr = logFile, logFile
	extractErr := command.Run()
	closeErr := logFile.Close()
	if extractErr != nil || closeErr != nil {
		return "", domain.Err("device_support_failed", "Golden VM dyld cache extraction failed; inspect the private DeviceSupport log")
	}
	if st, statErr := os.Lstat(filepath.Join(staging, "usr", "lib", "libobjc.A.dylib")); statErr != nil || !st.Mode().IsRegular() {
		return "", domain.Err("device_support_failed", "Extracted DeviceSupport is incomplete")
	}
	if _, statErr := os.Lstat(target); statErr == nil {
		invalid := fmt.Sprintf("%s.invalid-%d", target, time.Now().Unix())
		if err := os.Rename(target, invalid); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err := os.Rename(staging, target); err != nil {
		return "", err
	}
	complete = true
	return target, nil
}
