package catalog

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture() domain.ImageCatalog {
	return domain.ImageCatalog{MacOS: []domain.MacOSRelease{
		{Version: "12.5.1", Build: "21G83", URL: "https://updates.cdn-apple.com/old.ipsw", SHA256: strings.Repeat("1", 64), Size: 123},
		{Version: "12.6", Build: "21G115", URL: "https://updates.cdn-apple.com/new.ipsw", SHA256: strings.Repeat("2", 64), Size: 124},
	}, Xcode: []domain.XcodeRelease{{Version: "13.4.1", Build: "13F100", Requires: "12.0", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip", SHA1: strings.Repeat("a", 40)}, {Version: "16.0", Build: "16A1", Requires: "14.5", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_16/Xcode_16.xip", SHA1: strings.Repeat("b", 40)}}}
}
func TestResolveVersionsAndCompatibility(t *testing.T) {
	for _, tc := range []struct{ mac, xc, build, code string }{
		{"Monterey", "13.4.1", "21G115", ""}, {"12.5.1", "", "21G83", ""}, {"21G115", "13.4.1", "21G115", ""},
		{"monterey", "16.0", "", "incompatible_versions"}, {"99.0", "", "", "version_not_found"}, {"monterey", "99.0", "", "version_not_found"},
	} {
		p, err := Resolve(fixture(), domain.ImageCreateRequest{ID: "golden", MacOS: tc.mac, Xcode: tc.xc})
		if tc.code != "" {
			if e, ok := err.(*domain.Error); !ok || e.Code != tc.code {
				t.Fatalf("%+v: %v", tc, err)
			}
			continue
		}
		if err != nil || p.Build != tc.build || p.DisableSIP {
			t.Fatalf("%+v: %+v %v", tc, p, err)
		}
		if tc.xc != "" && (p.Provision != "developer-v1" || p.Xcode.Version != tc.xc) {
			t.Fatal(p)
		}
	}
}
func TestCatalogFiltersUnsafeArtifactsAndCaches(t *testing.T) {
	var calls atomic.Int32
	c := New()
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		b := `{"firmwares":[{"version":"12.6","buildid":"21G115","url":"https://updates.cdn-apple.com/mac.ipsw","sha256sum":"` + strings.Repeat("a", 64) + `","filesize":123},{"version":"12.7","buildid":"bad","url":"https://attacker.invalid/mac.ipsw","sha256sum":"` + strings.Repeat("b", 64) + `","filesize":123}]}`
		if r.URL.Host == "xcodereleases.com" {
			b = `[{"version":{"number":"13.4.1","build":"13F100","release":{"release":true}},"requires":"12.0","checksums":{"sha1":"` + strings.Repeat("a", 40) + `"},"links":{"download":{"url":"https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip"}}}]`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(b))}, nil
	})}
	for range 2 {
		v, err := c.List(context.Background())
		if err != nil || len(v.MacOS) != 1 || len(v.Xcode) != 1 {
			t.Fatal(v, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
func TestCatalogFailureIsSafe(t *testing.T) {
	c := New()
	c.HTTP = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("private diagnostic"))}, nil
	})}
	_, err := c.List(context.Background())
	if e, ok := err.(*domain.Error); !ok || e.Code != "catalog_unavailable" || strings.Contains(e.Message, "private") {
		t.Fatal(err)
	}
}
