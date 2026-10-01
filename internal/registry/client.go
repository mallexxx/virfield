// Package registry resolves GHCR references to verified immutable manifests.
// It never accepts a registry host, token or path from an API caller.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type Client struct {
	sources map[string]domain.RegistrySource
	http    *http.Client
	base    string
}

func New(sources []domain.RegistrySource) (*Client, error) {
	c := &Client{sources: map[string]domain.RegistrySource{}, base: "https://ghcr.io", http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	for _, s := range sources {
		if err := s.Validate(); err != nil {
			return nil, err
		}
		if _, ok := c.sources[s.ID]; ok {
			return nil, domain.Err("invalid_registry", "Duplicate registry source ID")
		}
		c.sources[s.ID] = s
	}
	return c, nil
}

// Sources deliberately excludes credential identity and paths.
func (c *Client) Sources() []map[string]any {
	out := []map[string]any{}
	for _, s := range c.sources {
		out = append(out, map[string]any{"id": s.ID, "registry": "ghcr.io", "organization": s.Organization, "authenticated": s.TokenFile != "", "allow_push": s.AllowPush})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}
func (c *Client) Source(id string) (domain.RegistrySource, error) {
	s, ok := c.sources[id]
	if !ok {
		return s, domain.Err("unknown_registry", "Registry source is not configured by the host operator")
	}
	return s, nil
}
func Credentials(s domain.RegistrySource) (string, error) {
	if s.TokenFile == "" {
		return "", nil
	}
	f, err := os.Open(s.TokenFile)
	if err != nil {
		return "", domain.Err("registry_auth_required", "Cannot read configured registry token file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", domain.Err("registry_auth_required", "Registry token must be a private regular file (mode 0600)")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", domain.Err("registry_auth_required", "Cannot read configured registry token file")
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 1 || len(token) > 4096 || strings.ContainsAny(token, " \r\n\t") {
		return "", domain.Err("registry_auth_required", "Registry token file is invalid")
	}
	return token, nil
}
func (c *Client) request(ctx context.Context, path, auth string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return nil, nil, err
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, nil, domain.Err("registry_unavailable", "Cannot reach GHCR; no mutation was started")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, nil, domain.Err("registry_auth_required", "GHCR access denied; check configured token and package permissions")
	}
	if res.StatusCode == 404 {
		return nil, nil, domain.Err("registry_not_found", "GHCR image or tag not found")
	}
	if res.StatusCode != 200 {
		return nil, nil, domain.Err("registry_unavailable", fmt.Sprintf("GHCR metadata returned HTTP %d", res.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(b) > 4<<20 {
		return nil, nil, domain.Err("registry_invalid", "GHCR metadata is incomplete or exceeds 4 MiB")
	}
	return b, res.Header, nil
}
func (c *Client) Resolve(ctx context.Context, r domain.RegistryResolveRequest) (domain.RegistryReference, error) {
	var ref domain.RegistryReference
	if err := r.Validate(); err != nil {
		return ref, err
	}
	source, err := c.Source(r.Source)
	if err != nil {
		return ref, err
	}
	secret, err := Credentials(source)
	if err != nil {
		return ref, err
	}
	// GHCR's token endpoint is fixed, never copied from an untrusted challenge.
	auth := ""
	if secret != "" {
		req := &http.Request{Header: http.Header{}}
		req.SetBasicAuth(source.Username, secret)
		auth = req.Header.Get("Authorization")
	}
	repository := source.Organization + "/" + r.Repository
	query := url.Values{"service": {"ghcr.io"}, "scope": {"repository:" + repository + ":pull"}}
	b, _, err := c.request(ctx, "/token?"+query.Encode(), auth)
	if err != nil {
		return ref, err
	}
	var token struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(b, &token) != nil || token.Token == "" || strings.ContainsAny(token.Token, "\r\n") {
		return ref, domain.Err("registry_invalid", "GHCR returned an invalid authorization response")
	}
	b, h, err := c.request(ctx, "/v2/"+repository+"/manifests/"+r.Tag, "Bearer "+token.Token)
	if err != nil {
		return ref, err
	}
	sum := sha256.Sum256(b)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if h.Get("Docker-Content-Digest") != "" && h.Get("Docker-Content-Digest") != digest {
		return ref, domain.Err("registry_digest_mismatch", "GHCR manifest digest verification failed")
	}
	var manifest struct {
		Schema    int          `json:"schemaVersion"`
		MediaType string       `json:"mediaType"`
		Config    *descriptor  `json:"config"`
		Layers    []descriptor `json:"layers"`
	}
	if json.Unmarshal(b, &manifest) != nil || manifest.Schema != 2 || len(manifest.Layers) == 0 || len(manifest.Layers) > 4096 {
		return ref, domain.Err("registry_invalid", "Expected a single VM image manifest, not an image index")
	}
	if err := validateMediaTypes(manifest.MediaType, manifest.Config, manifest.Layers); err != nil {
		return ref, err
	}
	total := int64(0)
	layers := manifest.Layers
	if manifest.Config != nil {
		layers = append(layers, *manifest.Config)
	}
	for _, d := range layers {
		if !domain.ValidRegistryDigest(d.Digest) || d.Size < 0 || d.Size > 512<<30 || total > 512<<30-d.Size {
			return ref, domain.Err("registry_invalid", "Invalid or oversized GHCR layer descriptor")
		}
		total += d.Size
	}
	ref = domain.RegistryReference{Source: source.ID, Organization: source.Organization, Repository: r.Repository, Tag: r.Tag, Digest: digest, Size: total}
	return ref, ref.Validate()
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Lume has two supported wire formats. Docker containers, Tart images and
// indexes are not interchangeable with a Lume macOS disk bundle.
func validateMediaTypes(media string, config *descriptor, layers []descriptor) error {
	invalid := func() error {
		return domain.Err("registry_format_unsupported", "Expected a Lume VM manifest with config, NVRAM and disk layers")
	}
	if media != "application/vnd.oci.image.manifest.v1+json" && media != "application/vnd.docker.distribution.manifest.v2+json" {
		return invalid()
	}
	modern := config != nil && config.MediaType == "application/vnd.trycua.lume.config.v1+json"
	configs, nvrams, disks := 0, 0, 0
	if modern {
		configs++
	}
	for _, layer := range layers {
		switch layer.MediaType {
		case "application/vnd.trycua.lume.disk.v1":
			if !modern {
				return invalid()
			}
			disks++
		case "application/vnd.trycua.lume.nvram.v1":
			if !modern || layer.Size > 64<<20 {
				return invalid()
			}
			nvrams++
		case "application/vnd.oci.image.config.v1+json":
			if modern || layer.Size > 1<<20 {
				return invalid()
			}
			configs++
		case "application/octet-stream":
			if modern || layer.Size > 64<<20 {
				return invalid()
			}
			nvrams++
		case "application/octet-stream+lz4":
			if modern {
				return invalid()
			}
			disks++
		default:
			return invalid()
		}
	}
	if config != nil && (config.Size > 1<<20 || (!modern && config.MediaType != "application/vnd.oci.image.config.v1+json" && config.MediaType != "application/vnd.oci.empty.v1+json")) {
		return invalid()
	}
	if configs != 1 || nvrams != 1 || disks == 0 {
		return invalid()
	}
	return nil
}

func (c *Client) PushTarget(id string) (string, error) {
	source, err := c.Source(id)
	if err != nil {
		return "", err
	}
	if !source.AllowPush {
		return "", domain.Err("registry_push_disabled", "Publishing is disabled for this registry source")
	}
	if _, err := Credentials(source); err != nil {
		return "", err
	}
	return source.Organization, nil
}
