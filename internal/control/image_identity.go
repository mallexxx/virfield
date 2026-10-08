package control

import "github.com/mallexxx/virfield/internal/domain"

// imageFingerprint preserves the v2 manifest identity even if API-only fields
// are later added to ImageProfile, XcodeRelease or RegistryReference.
func imageFingerprint(p *domain.ImageProfile) string {
	if p == nil {
		return ""
	}
	type registry struct {
		Source       string `json:"source"`
		Organization string `json:"organization"`
		Repository   string `json:"repository"`
		Tag          string `json:"tag"`
		Digest       string `json:"digest"`
		Size         int64  `json:"size"`
	}
	type xcode struct {
		Version  string `json:"version"`
		Build    string `json:"build"`
		Requires string `json:"requires_macos"`
		URL      string `json:"url"`
		SHA1     string `json:"sha1"`
	}
	type tart struct {
		Upstream string `json:"upstream"`
	}
	type node struct {
		Golden string `json:"golden"`
	}
	type identity struct {
		Registry   *registry `json:"registry,omitempty"`
		MacOS      string    `json:"macos,omitempty"`
		Xcode      *xcode    `json:"xcode,omitempty"`
		Security   string    `json:"security,omitempty"`
		Provision  string    `json:"provision,omitempty"`
		URL        string    `json:"url"`
		SHA256     string    `json:"sha256"`
		Size       int64     `json:"size"`
		Build      string    `json:"build"`
		DisableSIP bool      `json:"disable_sip"`
		Tart       *tart     `json:"tart,omitempty"`
		Node       *node     `json:"node,omitempty"`
	}
	v := identity{MacOS: p.MacOS, Security: p.Security, Provision: p.Provision, URL: p.URL, SHA256: p.SHA256, Size: p.Size, Build: p.Build, DisableSIP: p.DisableSIP}
	if p.Registry != nil {
		r := p.Registry
		v.Registry = &registry{r.Source, r.Organization, r.Repository, r.Tag, r.Digest, r.Size}
	}
	if p.Xcode != nil {
		x := p.Xcode
		v.Xcode = &xcode{x.Version, x.Build, x.Requires, x.URL, x.SHA1}
	}
	if p.Tart != nil {
		v.Tart = &tart{p.Tart.Upstream}
	}
	if p.Node != nil {
		v.Node = &node{p.Node.Golden}
	}
	return fingerprint(v)
}
