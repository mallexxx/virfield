// Package legacyios owns the opt-in, immutable Monterey legacy iOS toolchain.
package legacyios

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/qualify"
)

const Name = "legacy-ios-v1"
const Prefix = "/opt/virfield/toolchains/legacy-ios-v1"

//go:embed assets
var assets embed.FS

// Digest includes all pins, installer, checks and the representative fixture.
// It is separate from the ordinary image recipe, preserving existing profiles.
func Digest() string {
	h := sha256.New()
	_ = fs.WalkDir(assets, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(data))
		h.Write(data)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

func validate(p domain.ImageProfile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.Toolchain != Name || p.ToolchainSHA256 != Digest() {
		return domain.Err("unsupported_toolchain", "Toolchain recipe digest differs from this immutable image manifest")
	}
	return nil
}

func payload() ([]byte, error) {
	var out bytes.Buffer
	writer := tar.NewWriter(&out)
	err := fs.WalkDir(assets, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		if err := writer.WriteHeader(&tar.Header{Name: strings.TrimPrefix(path, "assets/"), Mode: 0644, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	data := []byte(Digest() + "\n")
	if err := writer.WriteHeader(&tar.Header{Name: "profile.sha256", Mode: 0644, Size: int64(len(data))}); err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Install runs only on a new owned image clone, before securing/reboot. Existing
// or partial toolchains are preserved; they are never repaired or overwritten.
func Install(ctx context.Context, g qualify.Guest, p domain.ImageProfile, log func(string) error) error {
	if p.Toolchain == "" {
		return nil
	}
	if err := validate(p); err != nil {
		return err
	}
	data, err := payload()
	if err != nil {
		return err
	}
	script := `set -eu
set +x
stage=$(/usr/bin/mktemp -d /private/tmp/virfield-legacy-stage.XXXXXX)
trap '/bin/rm -rf "$stage"' EXIT
printf '%s' '` + base64.StdEncoding.EncodeToString(data) + `' | /usr/bin/base64 -D | /usr/bin/tar -xf - -C "$stage"
sudo -n /bin/bash "$stage/install.sh" "$stage"
`
	out, err := g.RunReader(ctx, 90*time.Minute, "/bin/bash -c "+qualify.Quote(script), nil)
	if logErr := log(out); logErr != nil {
		return logErr
	}
	if err != nil {
		return domain.Err("toolchain_install_failed", "Pinned legacy iOS setup failed; preserve the private log and incomplete clone")
	}
	return nil
}

// Verify runs the native libraries and actual CocoaPods armv7 build after reboot.
// Its temporary build directory never touches the caller workspace or lockfiles.
func Verify(ctx context.Context, g qualify.Guest, p domain.ImageProfile, log func(string) error) (map[string]any, error) {
	if p.Toolchain == "" {
		return nil, nil
	}
	if err := validate(p); err != nil {
		return nil, err
	}
	command := "test \"$(/bin/cat " + Prefix + "/profile.sha256)\" = " + qualify.Quote(p.ToolchainSHA256) + " && /bin/bash " + Prefix + "/verify.sh"
	out, err := g.RunReader(ctx, 15*time.Minute, command, nil)
	if logErr := log(out); logErr != nil {
		return nil, logErr
	}
	if err != nil || !strings.HasSuffix(strings.TrimSpace(out), "virfield-legacy-ios-armv7-ok") {
		return nil, domain.Err("toolchain_verification_failed", "Legacy iOS native libraries, frozen dependencies or armv7 compilation/link failed")
	}
	var evidence map[string]any
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, `{"profile":`) {
			if json.Unmarshal([]byte(line), &evidence) != nil {
				return nil, domain.Err("toolchain_verification_failed", "Invalid toolchain evidence")
			}
			break
		}
	}
	if evidence == nil || evidence["profile"] != Name || evidence["ruby"] != "3.3.12" || evidence["cocoapods"] != "1.16.2" || evidence["ffi"] != "1.17.4" || evidence["ethon"] != "0.18.0" || (evidence["architecture"] != "arm64" && evidence["architecture"] != "x86_64") {
		return nil, domain.Err("toolchain_verification_failed", "Toolchain evidence differs from pinned versions")
	}
	sum := sha256.Sum256([]byte(out))
	evidence["recipe_sha256"] = p.ToolchainSHA256
	evidence["verification_sha256"] = hex.EncodeToString(sum[:])
	evidence["armv7_build"] = true
	return evidence, nil
}
