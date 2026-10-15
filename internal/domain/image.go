package domain

import (
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

// ImageProfile is a resolved immutable manifest, never a free-form client command.
// The digest and size identify one immutable Apple restore image.
type ImageProfile struct {
	BuildDiskBytes  int64              `json:"build_disk_bytes,omitempty"`
	Tart            *TartSource        `json:"tart,omitempty"`
	Node            *NodeSource        `json:"node,omitempty"`
	Registry        *RegistryReference `json:"registry,omitempty"`
	MacOS           string             `json:"macos,omitempty"`
	Xcode           *XcodeRelease      `json:"xcode,omitempty"`
	Security        string             `json:"security,omitempty"`
	Provision       string             `json:"provision,omitempty"`
	Toolchain       string             `json:"toolchain,omitempty"`
	ToolchainSHA256 string             `json:"toolchain_sha256,omitempty"`
	URL             string             `json:"url"`
	SHA256          string             `json:"sha256"`
	Size            int64              `json:"size"`
	Build           string             `json:"build"`
	DisableSIP      bool               `json:"disable_sip"`
}

// NodeSource names a bootstrap golden in a node's goldens directory: a
// hand-installed macOS disk with the bootstrap account and SSH enabled.
// Virfield secures a clone of it into the golden that leases use.
type NodeSource struct {
	Golden string `json:"golden"`
}

// TartSource pins one upstream Tart OCI image. Only digest references on GHCR
// are accepted; a tag can move under a qualified golden.
type TartSource struct {
	Upstream string `json:"upstream"`
}

var tartUpstreamPattern = regexp.MustCompile(`^ghcr\.io/[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*@sha256:[0-9a-f]{64}$`)

func (s TartSource) Validate() error {
	if !tartUpstreamPattern.MatchString(s.Upstream) {
		return Err("invalid_profile", "Tart upstream must be a ghcr.io image pinned by sha256 digest")
	}
	return nil
}

func (p ImageProfile) Validate() error {
	if p.Toolchain != "" {
		digest, err := hex.DecodeString(p.ToolchainSHA256)
		if p.Toolchain != "legacy-ios-v1" || p.Tart == nil || p.MacOS != "12.7.6" || p.Xcode == nil || p.Xcode.Version != "13.4.1" || p.Xcode.Build != "13F100" || p.Provision != "developer-v1" || err != nil || len(digest) != 32 || p.ToolchainSHA256 != strings.ToLower(p.ToolchainSHA256) {
			return Err("unsupported_toolchain", "legacy-ios-v1 requires pinned macOS 12.7.6/Xcode 13.4.1 (13F100), Tart developer-v1 and a toolchain SHA-256")
		}
	} else if p.ToolchainSHA256 != "" {
		return Err("invalid_profile", "toolchain digest requires an explicit toolchain profile")
	}
	if p.BuildDiskBytes < 0 || p.BuildDiskBytes > 512<<30 || p.BuildDiskBytes != 0 && p.Tart == nil {
		return Err("invalid_profile", "Build disk bound is supported only for Tart and must be at most 512 GiB")
	}
	switch p.Provision {
	case "", "base-v1":
		if p.Xcode != nil {
			return Err("invalid_profile", "Xcode requires developer-v1 provisioning")
		}
	case "security-v1":
		if p.Security != "automation" || p.Xcode != nil {
			return Err("invalid_profile", "security-v1 requires automation security without Xcode")
		}
	case "developer-v1":
		if p.Xcode == nil {
			return Err("invalid_profile", "developer-v1 requires a pinned Xcode release")
		}
	case "uitest-27-v1":
		if !p.DisableSIP || p.Build != "26A428" || p.Xcode != nil {
			return Err("invalid_profile", "uitest-27-v1 requires build 26A428, disabled SIP and the operator's local Xcode")
		}
	default:
		return Err("invalid_profile", "unknown provisioning recipe")
	}
	if p.Security != "" && p.Security != "default" && p.Security != "sip-disabled" && p.Security != "automation" {
		return Err("invalid_profile", "Unknown guest security policy")
	}
	if (p.Security == "automation" || p.Security == "sip-disabled") && !p.DisableSIP {
		return Err("invalid_profile", "Selected guest security policy requires disabled SIP")
	}
	if p.Security == "default" && p.DisableSIP {
		return Err("invalid_profile", "Default security requires enabled SIP")
	}
	if p.Security == "automation" && p.Provision != "security-v1" && p.Provision != "developer-v1" {
		return Err("invalid_profile", "Automation security requires guest provisioning")
	}
	if p.DisableSIP && p.Build != "26A428" && !SupportsRecovery(p.MacOS) {
		return Err("unsupported_policy", "Recovery automation supports macOS 11 through 15, 26 and 27; specify an exact macOS version")
	}
	if p.MacOS != "" && !ValidVersion(p.MacOS) {
		return Err("invalid_profile", "invalid macOS version")
	}
	if p.Xcode != nil {
		if err := p.Xcode.Validate(); err != nil {
			return err
		}
		if !ValidVersion(p.MacOS) || CompareVersions(p.MacOS, p.Xcode.Requires) < 0 {
			return Err("incompatible_versions", "Selected Xcode requires macOS "+p.Xcode.Requires+" or newer")
		}
	}
	if p.Node != nil {
		if p.Tart != nil || p.Registry != nil || p.URL != "" || p.SHA256 != "" || p.Size != 0 || p.Provision != "" || p.Xcode != nil || !ValidVersion(p.MacOS) || !ValidName(p.Build) || !ValidName(p.Node.Golden) {
			return Err("invalid_profile", "Node images need expected macOS/build, a golden name, no provisioning and no competing source")
		}
		return nil
	}
	if p.Tart != nil {
		if p.URL != "" || p.SHA256 != "" || p.Size != 0 || !ValidVersion(p.MacOS) || !ValidName(p.Build) || (p.Provision != "developer-v1" && p.Provision != "uitest-27-v1" && p.Provision != "base-v1" && p.Provision != "security-v1") {
			return Err("invalid_profile", "Tart images need expected macOS/build, a supported recipe and no competing restore source")
		}
		if p.Registry != nil {
			if err := p.Registry.Validate(); err != nil {
				return err
			}
			if p.Tart.Upstream != "ghcr.io/"+p.Registry.Organization+"/"+p.Registry.Repository+"@"+p.Registry.Digest {
				return Err("invalid_profile", "Registry provenance must match the pinned Tart source")
			}
		}
		return p.Tart.Validate()
	}
	if p.Registry != nil {
		if p.URL != "" || p.SHA256 != "" || p.Size != 0 || !ValidVersion(p.MacOS) || !ValidName(p.Build) {
			return Err("invalid_profile", "Registry images need expected macOS/build and no competing IPSW source")
		}
		return p.Registry.Validate()
	}
	u, err := url.Parse(p.URL)
	if err != nil || u.Scheme != "https" || u.Host != "updates.cdn-apple.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, ".ipsw") {
		return Err("invalid_profile", "IPSW must be a pinned HTTPS restore image from updates.cdn-apple.com")
	}
	digest, err := hex.DecodeString(p.SHA256)
	if err != nil || len(digest) != 32 || p.SHA256 != strings.ToLower(p.SHA256) || p.Size < 1 || p.Size > 40<<30 || !ValidName(p.Build) {
		return Err("invalid_profile", "profile requires lowercase SHA-256, exact size (up to 40 GiB), and expected macOS build")
	}
	return nil
}

// SupportsRecovery lists Apple silicon guest generations with paired Recovery.
// Acceptance still requires signed policy success and canonical normal-boot status.
func SupportsRecovery(version string) bool {
	if !ValidVersion(version) {
		return false
	}
	switch strings.Split(version, ".")[0] {
	case "11", "12", "13", "14", "15", "26", "27":
		return true
	}
	return false
}
