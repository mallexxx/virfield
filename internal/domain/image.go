package domain

import (
	"encoding/hex"
	"net/url"
	"strings"
)

// ImageProfile is a resolved immutable manifest, never a free-form client command.
// The digest and size identify one immutable Apple restore image.
type ImageProfile struct {
	Registry   *RegistryReference `json:"registry,omitempty"`
	MacOS      string             `json:"macos,omitempty"`
	Xcode      *XcodeRelease      `json:"xcode,omitempty"`
	Security   string             `json:"security,omitempty"`
	Provision  string             `json:"provision,omitempty"`
	URL        string             `json:"url"`
	SHA256     string             `json:"sha256"`
	Size       int64              `json:"size"`
	Build      string             `json:"build"`
	DisableSIP bool               `json:"disable_sip"`
}

func (p ImageProfile) Validate() error {
	switch p.Provision {
	case "":
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

// ImageTools are immutable operator-owned host paths, never API input.
type ImageTools struct {
	Xcode         string `json:"xcode,omitempty"`
	XcodeArchives string `json:"xcode_archives,omitempty"`
	AppleCookies  string `json:"apple_cookies,omitempty"`
	Lume          string `json:"lume"`
	Python        string `json:"python"`
	Tesseract     string `json:"tesseract"`
	VNCBin        string `json:"vnc_bin"`
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
