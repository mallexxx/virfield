package domain

import (
	"encoding/hex"
	"net/url"
	"strings"
)

// ImageProfile is operator configuration, never a free-form client command.
// The digest and size identify one immutable Apple restore image.
type ImageProfile struct {
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Build      string `json:"build"`
	DisableSIP bool   `json:"disable_sip"`
}

func (p ImageProfile) Validate() error {
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
	Lume      string `json:"lume"`
	Python    string `json:"python"`
	Tesseract string `json:"tesseract"`
	VNCBin    string `json:"vnc_bin"`
}
