package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/legacyios"
)

type legacyMetadata struct{}

func (legacyMetadata) List(context.Context) (domain.ImageCatalog, error) {
	return domain.ImageCatalog{Xcode: []domain.XcodeRelease{{Version: "13.4.1", Build: "13F100", Requires: "12.0", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip", SHA1: strings.Repeat("a", 40)}}}, nil
}
func TestLegacyIOSOptInPinsRecipeAndRejectsUnsupportedOS(t *testing.T) {
	c, err := NewTart(legacyMetadata{}, []domain.TartBase{{MacOS: "12.7.6", Build: "21H1320", Upstream: "ghcr.io/team/base@sha256:" + strings.Repeat("a", 64)}, {MacOS: "13.7.4", Build: "22H420", Upstream: "ghcr.io/team/base@sha256:" + strings.Repeat("b", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	r := domain.ImageCreateRequest{ID: "legacy", MacOS: "monterey", Xcode: "13.4.1"}
	ordinary, err := c.Resolve(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(ordinary)
	if strings.Contains(string(data), "toolchain") {
		t.Fatal("ordinary image fingerprint changed", string(data))
	}
	r.Toolchain = legacyios.Name
	p, err := c.Resolve(context.Background(), r)
	if err != nil || p.ToolchainSHA256 != legacyios.Digest() {
		t.Fatal(p, err)
	}
	r.MacOS = "ventura"
	if _, err := c.Resolve(context.Background(), r); err == nil {
		t.Fatal("unsupported guest OS admitted")
	}
	if _, err := Resolve(domain.ImageCatalog{}, r); err == nil {
		t.Fatal("unsupported IPSW builder admitted")
	}
	r.Xcode = "14.0"
	if r.Validate() == nil {
		t.Fatal("unsupported Xcode admitted")
	}
}
