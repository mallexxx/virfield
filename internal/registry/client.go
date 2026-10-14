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
	"strconv"
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
		Schema      int               `json:"schemaVersion"`
		MediaType   string            `json:"mediaType"`
		Config      *descriptor       `json:"config"`
		Layers      []descriptor      `json:"layers"`
		Annotations map[string]string `json:"annotations"`
	}
	if json.Unmarshal(b, &manifest) != nil || manifest.Schema != 2 || len(manifest.Layers) == 0 || len(manifest.Layers) > 4096 {
		return ref, domain.Err("registry_invalid", "Expected a single VM image manifest, not an image index")
	}
	if err := validateMediaTypes(manifest.MediaType, manifest.Config, manifest.Layers); err != nil {
		return ref, err
	}
	if err := validateDiskCoverage(manifest.Annotations, manifest.Layers); err != nil {
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
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

const (
	tartConfig  = "application/vnd.cirruslabs.tart.config.v1"
	tartDisk    = "application/vnd.cirruslabs.tart.disk.v2"
	tartOverlay = "application/vnd.cirruslabs.tart.disk.asif.overlay.v1"
	tartNVRAM   = "application/vnd.cirruslabs.tart.nvram.v1"
	chunkSize   = "org.cirruslabs.tart.uncompressed-size"
	chunkDigest = "org.cirruslabs.tart.uncompressed-content-digest"
	fileDigest  = "org.cirruslabs.tart.disk-file-content-digest"
	chunkCount  = "org.cirruslabs.tart.disk-file-chunk-count"
	diskSize    = "org.cirruslabs.tart.uncompressed-disk-size"
)

// Tart's canonical layout is config, a disk.v2 base, optional ASIF overlay
// groups, then NVRAM. Disk reconstruction and blob verification remain in Tart.
func validateMediaTypes(media string, config *descriptor, layers []descriptor) error {
	invalid := domain.Err("registry_format_unsupported", "Expected a Tart VM manifest with ordered config, disk and NVRAM layers")
	if media != "application/vnd.oci.image.manifest.v1+json" || config == nil || config.MediaType != "application/vnd.oci.image.config.v1+json" || config.Size > 1<<20 || len(layers) < 3 {
		return invalid
	}
	if layers[0].MediaType != tartConfig || layers[0].Size > 1<<20 || layers[len(layers)-1].MediaType != tartNVRAM || layers[len(layers)-1].Size > 64<<20 || layers[1].MediaType != tartDisk {
		return invalid
	}
	for _, l := range layers[1 : len(layers)-1] {
		if l.MediaType != tartDisk && l.MediaType != tartOverlay {
			return invalid
		}
	}
	return nil
}

func validateDiskCoverage(annotations map[string]string, layers []descriptor) error {
	invalid := domain.Err("registry_invalid", "Tart disk chunk metadata is incomplete or inconsistent")
	if len(layers) < 3 {
		return invalid
	}
	chunks := layers[1 : len(layers)-1]
	total := uint64(0)
	for _, l := range chunks {
		size, err := strconv.ParseUint(l.Annotations[chunkSize], 10, 64)
		if err != nil || size == 0 || size > 512<<30 || total > 512<<30-size || !domain.ValidRegistryDigest(l.Annotations[chunkDigest]) {
			return invalid
		}
		total += size
	}
	base := 0
	for base < len(chunks) && chunks[base].MediaType == tartDisk {
		base++
	}
	if base == 0 || chunks[0].Annotations[chunkCount] != "" {
		return invalid
	}
	if d := chunks[0].Annotations[fileDigest]; d != "" && !domain.ValidRegistryDigest(d) {
		return invalid
	}
	for _, l := range chunks[1:base] {
		if l.Annotations[fileDigest] != "" || l.Annotations[chunkCount] != "" {
			return invalid
		}
	}
	if base < len(chunks) && !domain.ValidRegistryDigest(chunks[0].Annotations[fileDigest]) {
		return invalid
	}
	for i := base; i < len(chunks); {
		first := chunks[i]
		n, err := strconv.ParseUint(first.Annotations[chunkCount], 10, 64)
		if err != nil || n == 0 || n > uint64(len(chunks)-i) || first.MediaType != tartOverlay || !domain.ValidRegistryDigest(first.Annotations[fileDigest]) {
			return invalid
		}
		for j := i; j < i+int(n); j++ {
			if chunks[j].MediaType != tartOverlay || j > i && (chunks[j].Annotations[fileDigest] != "" || chunks[j].Annotations[chunkCount] != "") {
				return invalid
			}
		}
		i += int(n)
	}
	// For flat images the optional manifest size identifies the reconstructed disk.
	// Stacked ASIF images retain the base's logical size; overlays have their own
	// whole-file sizes and must not be added to that logical size.
	if declared := annotations[diskSize]; declared != "" {
		n, err := strconv.ParseUint(declared, 10, 64)
		if err != nil || n == 0 || n > 512<<30 || base == len(chunks) && n != total {
			return invalid
		}
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
