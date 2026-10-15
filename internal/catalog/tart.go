package catalog

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/legacyios"
	"sort"
	"strings"
)

type Metadata interface {
	List(context.Context) (domain.ImageCatalog, error)
}
type TartCatalog struct {
	metadata Metadata
	bases    []domain.TartBase
}

func NewTart(metadata Metadata, bases []domain.TartBase) (*TartCatalog, error) {
	seen := map[string]int{}
	valid := []domain.TartBase{}
	for _, b := range bases {
		if err := b.Validate(); err != nil {
			return nil, err
		}
		key := b.Upstream
		if index, exists := seen[key]; exists {
			previous := valid[index]
			if previous.MacOS != b.MacOS || previous.Build != b.Build || previous.DisableSIP != b.DisableSIP || previous.DiskBytes != 0 && b.DiskBytes != 0 && previous.DiskBytes != b.DiskBytes {
				return nil, domain.Err("invalid_profile", "A pinned Tart upstream has conflicting OS, SIP or disk metadata")
			}
			valid[index].DiskBytes = max(previous.DiskBytes, b.DiskBytes)
		} else {
			seen[key] = len(valid)
			valid = append(valid, b)
		}
	}
	sort.Slice(valid, func(i, j int) bool { return domain.CompareVersions(valid[i].MacOS, valid[j].MacOS) > 0 })
	return &TartCatalog{metadata: metadata, bases: valid}, nil
}

func (c *TartCatalog) List(ctx context.Context) (domain.ImageCatalog, error) {
	v, err := c.metadata.List(ctx)
	if err != nil {
		v = domain.ImageCatalog{MacOS: []domain.MacOSRelease{}, Xcode: []domain.XcodeRelease{}, MetadataError: domain.Err("catalog_unavailable", "Public metadata is unavailable; configured image inventory remains usable")}
	}
	v.TartBases = append([]domain.TartBase(nil), c.bases...)
	return v, nil
}

func (c *TartCatalog) Resolve(ctx context.Context, r domain.ImageCreateRequest) (domain.ImageProfile, error) {
	if err := r.Validate(); err != nil {
		return domain.ImageProfile{}, err
	}
	security := r.Security
	if security == "" {
		security = "default"
	}
	selector := strings.ToLower(r.MacOS)
	major, isAlias := aliases[selector]
	var selected *domain.TartBase
	foundOS := false
	for _, b := range c.bases {
		match := b.MacOS == r.MacOS || strings.EqualFold(b.Build, r.MacOS) || (isAlias && strings.Split(b.MacOS, ".")[0] == major)
		if !match {
			continue
		}
		foundOS = true
		if b.DisableSIP != (security == "sip-disabled" || security == "automation") {
			continue
		}
		if selected == nil || domain.CompareVersions(b.MacOS, selected.MacOS) > 0 {
			copy := b
			selected = &copy
		}
	}
	if selected == nil {
		if foundOS {
			return domain.ImageProfile{}, domain.Err("unsupported_policy", "No approved Tart base matches the requested SIP policy")
		}
		return domain.ImageProfile{}, domain.Err("version_not_found", "Requested macOS/build is not in the operator-approved Tart base catalog")
	}
	p := domain.ImageProfile{BuildDiskBytes: selected.DiskBytes, Tart: &domain.TartSource{Upstream: selected.Upstream}, MacOS: selected.MacOS, Build: selected.Build, DisableSIP: selected.DisableSIP, Security: security, Provision: "base-v1"}
	if security == "automation" {
		p.Provision = "security-v1"
	}
	if r.Xcode != "" {
		v, err := c.List(ctx)
		if err != nil {
			return p, err
		}
		if v.MetadataError != nil {
			return p, v.MetadataError
		}
		for _, x := range v.Xcode {
			if x.Version == r.Xcode {
				copy := x
				copy.LocalState = ""
				copy.InstalledImages = nil
				p.Xcode = &copy
				p.Provision = "developer-v1"
				break
			}
		}
		if p.Xcode == nil {
			return p, domain.Err("version_not_found", "Requested stable Xcode is absent from public metadata")
		}
	}
	if r.Toolchain != "" {
		p.Toolchain = r.Toolchain
		p.ToolchainSHA256 = legacyios.Digest()
	}
	return p, p.Validate()
}
