package domain

import (
	"encoding/hex"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var versionPattern = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3}){0,3}$`)

func ValidVersion(s string) bool { return versionPattern.MatchString(s) }
func CompareVersions(a, b string) int {
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(aa), len(bb)); i++ {
		x, y := 0, 0
		if i < len(aa) {
			x, _ = strconv.Atoi(aa[i])
		}
		if i < len(bb) {
			y, _ = strconv.Atoi(bb[i])
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

type XcodeRelease struct {
	Version  string `json:"version"`
	Build    string `json:"build"`
	Requires string `json:"requires_macos"`
	URL      string `json:"url"`
	SHA1     string `json:"sha1"`
	// Catalog-only snapshot. These fields are never used to resolve or pin a build.
	LocalState      string   `json:"local_state,omitempty"`
	InstalledImages []string `json:"installed_images,omitempty"`
}

func (x XcodeRelease) Validate() error {
	u, err := url.Parse(x.URL)
	hash, e := hex.DecodeString(x.SHA1)
	if err != nil || u.Scheme != "https" || u.Host != "download.developer.apple.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/Developer_Tools/") || !strings.HasSuffix(u.Path, ".xip") || e != nil || len(hash) != 20 || x.SHA1 != strings.ToLower(x.SHA1) || !ValidVersion(x.Version) || !ValidVersion(x.Requires) || !ValidName(x.Build) {
		return Err("invalid_profile", "Xcode requires an Apple Developer XIP URL, release version, build, minimum macOS and checksum")
	}
	return nil
}

type MacOSRelease struct {
	Version string `json:"version"`
	Build   string `json:"build"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}
type ImageCatalog struct {
	MacOS         []MacOSRelease `json:"macos"`
	Xcode         []XcodeRelease `json:"xcode"`
	Inventory     ImageInventory `json:"inventory"`
	MetadataError *Error         `json:"metadata_error,omitempty"`
	TartBases     []TartBase     `json:"tart_bases,omitempty"`
}

// TartBase pins an operator-approved upstream and its exact OS/build/SIP.
type TartBase struct {
	DiskBytes  int64  `json:"disk_bytes,omitempty"`
	MacOS      string `json:"macos"`
	Build      string `json:"build"`
	Upstream   string `json:"upstream"`
	DisableSIP bool   `json:"disable_sip"`
}

func (b TartBase) Validate() error {
	if !ValidVersion(b.MacOS) || !ValidName(b.Build) || b.DiskBytes < 0 || b.DiskBytes > 512<<30 {
		return Err("invalid_profile", "Tart base requires exact macOS and build")
	}
	return (TartSource{Upstream: b.Upstream}).Validate()
}

// ImageInventory is a narrow, read-only snapshot for image builders. A
// configured image is only a recipe until its VM is observed and built.
type ImageInventory struct {
	Verified         bool              `json:"verified"`
	ObservedAt       time.Time         `json:"observed_at"`
	VMs              []InventoryVM     `json:"vms"`
	ConfiguredImages []ConfiguredImage `json:"configured_images"`
}

type InventoryVM struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	OS       string `json:"os,omitempty"`
	State    string `json:"state"`
}

type ConfiguredImage struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	MacOS    string `json:"macos,omitempty"`
	Xcode    string `json:"xcode,omitempty"`
	Present  bool   `json:"present"`
	Ready    bool   `json:"ready"`
}

// ImageCreateRequest selects catalog versions. Host paths and arbitrary URLs are
// intentionally absent. Storage is restricted to the operator's named locations.
type ImageCreateRequest struct {
	ID        string `json:"id"`
	MacOS     string `json:"macos"`
	Xcode     string `json:"xcode,omitempty"`
	Location  string `json:"location,omitempty"`
	Security  string `json:"security,omitempty"`
	Toolchain string `json:"toolchain,omitempty"`
}

func (r ImageCreateRequest) Validate() error {
	if r.Toolchain != "" && r.Toolchain != "legacy-ios-v1" {
		return Err("unsupported_toolchain", "toolchain must be omitted or legacy-ios-v1")
	}
	if r.Toolchain != "" && r.Xcode != "13.4.1" {
		return Err("unsupported_toolchain", "legacy-ios-v1 requires an explicit Xcode 13.4.1 selection")
	}
	if r.Security != "" && r.Security != "default" && r.Security != "sip-disabled" && r.Security != "automation" {
		return Err("invalid_request", "security must be default, sip-disabled or automation")
	}
	if !ValidName(r.ID) || !ValidName(r.MacOS) || (r.Xcode != "" && !ValidVersion(r.Xcode)) || (r.Location != "" && !ValidName(r.Location)) {
		return Err("invalid_request", "Specify a valid image ID, macOS version/build/codename and optional exact Xcode version and storage name")
	}
	return nil
}
