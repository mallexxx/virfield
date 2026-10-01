// Package catalog resolves version selectors into pinned Apple artifacts.
package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

const macOSURL = "https://api.ipsw.me/v4/device/VirtualMac2,1?type=ipsw"
const xcodeURL = "https://xcodereleases.com/data.json"

type Catalog struct {
	HTTP    *http.Client
	mu      sync.Mutex
	cached  domain.ImageCatalog
	updated time.Time
}

func New() *Catalog {
	return &Catalog{HTTP: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Catalog) fetch(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return &domain.Error{Code: "catalog_unavailable", Message: "Version catalog unavailable; retry when network access recovers", Retryable: true}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return &domain.Error{Code: "catalog_unavailable", Message: "Version catalog returned an unexpected response", Retryable: res.StatusCode >= 500 || res.StatusCode == 429 || res.StatusCode == 408}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return domain.Err("catalog_unavailable", "Version catalog response is incomplete or too large")
	}
	if json.Unmarshal(b, v) != nil {
		return domain.Err("catalog_unavailable", "Version catalog response is invalid")
	}
	return nil
}
func (c *Catalog) List(ctx context.Context) (domain.ImageCatalog, error) {
	c.mu.Lock()
	if time.Since(c.updated) < 10*time.Minute {
		v := c.cached
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	var mac struct {
		Firmwares []struct {
			Version string
			Build   string `json:"buildid"`
			URL     string
			SHA256  string `json:"sha256sum"`
			Size    int64  `json:"filesize"`
		}
	}
	var xc []struct {
		Version struct {
			Number  string
			Build   string
			Release struct {
				Stable bool `json:"release"`
			}
		}
		Requires  string
		Checksums struct{ SHA1 string }
		Links     struct{ Download struct{ URL string } }
	}
	// Independent public metadata requests share the caller's deadline.
	ch := make(chan error, 2)
	go func() { ch <- c.fetch(ctx, macOSURL, &mac) }()
	go func() { ch <- c.fetch(ctx, xcodeURL, &xc) }()
	e1, e2 := <-ch, <-ch
	if e1 != nil {
		return domain.ImageCatalog{}, e1
	}
	if e2 != nil {
		return domain.ImageCatalog{}, e2
	}
	v := domain.ImageCatalog{MacOS: []domain.MacOSRelease{}, Xcode: []domain.XcodeRelease{}}
	for _, m := range mac.Firmwares {
		p := domain.ImageProfile{MacOS: m.Version, Build: m.Build, URL: m.URL, SHA256: m.SHA256, Size: m.Size}
		if !domain.ValidVersion(m.Version) || p.Validate() != nil {
			continue
		}
		v.MacOS = append(v.MacOS, domain.MacOSRelease{Version: m.Version, Build: m.Build, URL: m.URL, SHA256: m.SHA256, Size: m.Size})
	}
	for _, x := range xc {
		r := domain.XcodeRelease{Version: x.Version.Number, Build: x.Version.Build, Requires: x.Requires, URL: x.Links.Download.URL, SHA1: x.Checksums.SHA1}
		if x.Version.Release.Stable && r.Validate() == nil {
			v.Xcode = append(v.Xcode, r)
		}
	}
	if len(v.MacOS) == 0 || len(v.Xcode) == 0 {
		return domain.ImageCatalog{}, domain.Err("catalog_unavailable", "Catalog has no validated releases")
	}
	sort.Slice(v.MacOS, func(i, j int) bool { return domain.CompareVersions(v.MacOS[i].Version, v.MacOS[j].Version) > 0 })
	sort.Slice(v.Xcode, func(i, j int) bool { return domain.CompareVersions(v.Xcode[i].Version, v.Xcode[j].Version) > 0 })
	c.mu.Lock()
	c.cached = v
	c.updated = time.Now()
	c.mu.Unlock()
	return v, nil
}

var aliases = map[string]string{"bigsur": "11", "big-sur": "11", "monterey": "12", "ventura": "13", "sonoma": "14", "sequoia": "15", "tahoe": "26"}

func (c *Catalog) Resolve(ctx context.Context, r domain.ImageCreateRequest) (domain.ImageProfile, error) {
	if err := r.Validate(); err != nil {
		return domain.ImageProfile{}, err
	}
	v, err := c.List(ctx)
	if err != nil {
		return domain.ImageProfile{}, err
	}
	return Resolve(v, r)
}
func Resolve(v domain.ImageCatalog, r domain.ImageCreateRequest) (domain.ImageProfile, error) {
	var p domain.ImageProfile
	selector := strings.ToLower(r.MacOS)
	major, isAlias := aliases[selector]
	for _, m := range v.MacOS {
		match := strings.EqualFold(m.Build, r.MacOS) || m.Version == r.MacOS
		if isAlias {
			match = strings.Split(m.Version, ".")[0] == major
		}
		if match && (p.MacOS == "" || domain.CompareVersions(m.Version, p.MacOS) > 0) {
			p = domain.ImageProfile{MacOS: m.Version, Build: m.Build, URL: m.URL, SHA256: m.SHA256, Size: m.Size}
		}
	}
	if p.MacOS == "" {
		return p, domain.Err("version_not_found", "Requested macOS is absent from the Apple silicon restore catalog; use image_catalog for available versions")
	}
	if r.Xcode != "" {
		for _, x := range v.Xcode {
			if x.Version == r.Xcode {
				copy := x
				p.Xcode = &copy
				p.Provision = "developer-v1"
				break
			}
		}
		if p.Xcode == nil {
			return p, domain.Err("version_not_found", "Requested stable Xcode version is absent from the catalog")
		}
	}
	return p, p.Validate()
}
