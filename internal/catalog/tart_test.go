package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

type tartMetadata struct{ fail bool }

func (m tartMetadata) List(context.Context) (domain.ImageCatalog, error) {
	if m.fail {
		return domain.ImageCatalog{}, errors.New("offline")
	}
	return domain.ImageCatalog{Xcode: []domain.XcodeRelease{{Version: "15.2", Build: "15C500b", Requires: "13.5", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_15.2/Xcode_15.2.xip", SHA1: strings.Repeat("a", 40)}}}, nil
}
func TestTartCatalogPinsConfiguredBaseAndRejectsMissingSIPPolicy(t *testing.T) {
	c, err := NewTart(tartMetadata{}, []domain.TartBase{{MacOS: "13.7.4", Build: "22H420", Upstream: "ghcr.io/team/base@sha256:" + strings.Repeat("a", 64), DisableSIP: true}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Resolve(context.Background(), domain.ImageCreateRequest{ID: "test", MacOS: "ventura", Xcode: "15.2", Security: "automation"})
	if err != nil || p.Tart == nil || p.URL != "" || p.Provision != "developer-v1" {
		t.Fatal(p, err)
	}
	_, err = c.Resolve(context.Background(), domain.ImageCreateRequest{ID: "test", MacOS: "ventura", Security: "default"})
	if e, ok := err.(*domain.Error); !ok || e.Code != "unsupported_policy" {
		t.Fatal(err)
	}
}
func TestTartCatalogKeepsConfiguredBasesWhenMetadataIsOffline(t *testing.T) {
	c, err := NewTart(tartMetadata{fail: true}, []domain.TartBase{{MacOS: "13.7.4", Build: "22H420", Upstream: "ghcr.io/team/base@sha256:" + strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.List(context.Background())
	if err != nil || v.MetadataError == nil || len(v.TartBases) != 1 {
		t.Fatal(v, err)
	}
	p, err := c.Resolve(context.Background(), domain.ImageCreateRequest{ID: "test", MacOS: "22H420"})
	if err != nil || p.Provision != "base-v1" {
		t.Fatal(p, err)
	}
	_, err = c.Resolve(context.Background(), domain.ImageCreateRequest{ID: "test", MacOS: "22H420", Xcode: "15.2"})
	if e, ok := err.(*domain.Error); !ok || e.Code != "catalog_unavailable" {
		t.Fatal(err)
	}
}

func TestTartCatalogRejectsConflictingPinnedBaseMetadata(t *testing.T) {
	b := domain.TartBase{MacOS: "13.7.4", Build: "22H420", Upstream: "ghcr.io/team/base@sha256:" + strings.Repeat("a", 64)}
	other := b
	other.DisableSIP = true
	if _, err := NewTart(tartMetadata{}, []domain.TartBase{b, other}); err == nil {
		t.Fatal("conflicting pinned metadata accepted")
	}
}
