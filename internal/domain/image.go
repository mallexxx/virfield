package domain

import (
	"encoding/hex"
	"net/url"
	"strings"
)

// ImageProfile is a resolved immutable manifest, never a free-form client command.
// The digest and size identify one immutable Apple restore image.
type ImageProfile struct {
	MacOS      string        `json:"macos,omitempty"`
	Xcode      *XcodeRelease `json:"xcode,omitempty"`
	Provision  string        `json:"provision,omitempty"`
	URL        string        `json:"url"`
	SHA256     string        `json:"sha256"`
	Size       int64         `json:"size"`
	Build      string        `json:"build"`
	DisableSIP bool          `json:"disable_sip"`
}

func (p ImageProfile) Validate() error {
	switch p.Provision {
	case "":
		if p.Xcode != nil {
			return Err("invalid_profile", "Xcode requires developer-v1 provisioning")
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
	if p.DisableSIP && p.Build != "26A428" {
		return Err("unsupported_policy", "Automated SIP removal is currently verified only for build 26A428; other macOS versions can be built with SIP enabled")
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

// ImageTools are immutable operator-owned executable paths, never API input.
type ImageTools struct {
	Xcode         string `json:"xcode,omitempty"`
	XcodeArchives string `json:"xcode_archives,omitempty"`
	AppleCookies  string `json:"apple_cookies,omitempty"`
	Lume          string `json:"lume"`
	Python        string `json:"python"`
	Tesseract     string `json:"tesseract"`
	VNCBin        string `json:"vnc_bin"`
}
