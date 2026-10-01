package domain

import (
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

// RegistrySource is operator-owned. Credentials and filesystem paths never come
// from an API request or enter public status, jobs or image manifests.
type RegistrySource struct {
	ID           string `json:"id"`
	Organization string `json:"organization"`
	Username     string `json:"username,omitempty"`
	TokenFile    string `json:"token_file,omitempty"`
	AllowPush    bool   `json:"allow_push,omitempty"`
}

var registryOrganization = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$`)
var registryRepository = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
var registryTag = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

func (s RegistrySource) Validate() error {
	if !ValidName(s.ID) || !registryOrganization.MatchString(s.Organization) {
		return Err("invalid_registry", "Registry source needs a valid ID and lowercase GitHub organization/user")
	}
	if (s.TokenFile == "") != (s.Username == "") || (s.TokenFile != "" && !filepath.IsAbs(s.TokenFile)) || (s.Username != "" && !registryOrganization.MatchString(strings.ToLower(s.Username))) {
		return Err("invalid_registry", "Registry credentials require a GitHub username and absolute private token_file")
	}
	if s.AllowPush && s.TokenFile == "" {
		return Err("invalid_registry", "Registry push requires configured credentials")
	}
	return nil
}
func ValidRegistryRepository(s string) bool {
	return len(s) <= 128 && registryRepository.MatchString(s)
}
func ValidRegistryTag(s string) bool { return registryTag.MatchString(s) }
func ValidRegistryDigest(s string) bool {
	if !strings.HasPrefix(s, "sha256:") || s != strings.ToLower(s) {
		return false
	}
	b, err := hex.DecodeString(strings.TrimPrefix(s, "sha256:"))
	return err == nil && len(b) == 32
}

type RegistryReference struct {
	Source       string `json:"source"`
	Organization string `json:"organization"`
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
	Digest       string `json:"digest"`
	Size         int64  `json:"size"`
}

func (r RegistryReference) Validate() error {
	if !ValidName(r.Source) || !registryOrganization.MatchString(r.Organization) || !ValidRegistryRepository(r.Repository) || !ValidRegistryTag(r.Tag) || !ValidRegistryDigest(r.Digest) || r.Size < 1 || r.Size > 512<<30 {
		return Err("invalid_registry", "Invalid pinned GHCR image reference")
	}
	return nil
}

type RegistryResolveRequest struct {
	Source     string `json:"source"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
}

func (r RegistryResolveRequest) Validate() error {
	if !ValidName(r.Source) || !ValidRegistryRepository(r.Repository) || !ValidRegistryTag(r.Tag) {
		return Err("invalid_request", "Select a configured registry source, repository name and explicit tag")
	}
	return nil
}

// ImagePullRequest pins the expected guest versions independently of registry
// metadata: downloading a VM never establishes its readiness or security policy.
type ImagePullRequest struct {
	ImageCreateRequest
	Source     string `json:"source"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
}

func (r ImagePullRequest) Validate() error {
	if err := r.ImageCreateRequest.Validate(); err != nil {
		return err
	}
	return (RegistryResolveRequest{Source: r.Source, Repository: r.Repository, Tag: r.Tag}).Validate()
}

type ImagePublishRequest struct {
	Template   string `json:"template"`
	Source     string `json:"source"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
}

func (r ImagePublishRequest) Validate() error {
	if !ValidName(r.Template) {
		return Err("invalid_request", "A registered template ID is required")
	}
	return (RegistryResolveRequest{Source: r.Source, Repository: r.Repository, Tag: r.Tag}).Validate()
}

// RegistryExport rebuilds a portable VM from a pinned recipe, never exports a
// managed disk containing private credentials, workloads or historical blocks.
type RegistryExport struct {
	Source       string `json:"source"`
	Organization string `json:"organization"`
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
	Digest       string `json:"digest,omitempty"`
}
