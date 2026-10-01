package registry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// Explicit read-only live probe. Resolves public metadata; never downloads disks,
// uploads files, reads credentials or changes any VM/service.
func TestLivePublicManifest(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_REGISTRY_METADATA") != "1" {
		t.Skip("set VIRFIELD_LIVE_REGISTRY_METADATA=1 for public GHCR metadata")
	}
	c, err := New([]domain.RegistrySource{{ID: "public-cua", Organization: "trycua"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ref, err := c.Resolve(ctx, domain.RegistryResolveRequest{Source: "public-cua", Repository: "macos-sequoia-vanilla", Tag: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("public manifest %s size=%d", ref.Digest, ref.Size)
}
