package catalog

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
	"os"
	"testing"
	"time"
)

func TestLiveVersionCatalog(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_CATALOG") != "1" {
		t.Skip("requires public metadata network access")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := New()
	v, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Resolve(ctx, domain.ImageCreateRequest{ID: "monterey-xcode", MacOS: "monterey", Xcode: "13.4.1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Xcode.Build != "13F100" || p.Xcode.Requires != "12.0" {
		t.Fatal(p.Xcode)
	}
	t.Logf("%d macOS restores, %d stable Xcode releases; Monterey -> %s (%s), Xcode -> %s (%s)", len(v.MacOS), len(v.Xcode), p.MacOS, p.Build, p.Xcode.Version, p.Xcode.Build)
}
