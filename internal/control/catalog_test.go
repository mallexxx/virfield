package control

import (
	"context"
	"encoding/json"
	"github.com/mallexxx/virfield/internal/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

type statusCatalog struct{ list domain.ImageCatalog }

func (s statusCatalog) List(context.Context) (domain.ImageCatalog, error) { return s.list, nil }
func (s statusCatalog) Resolve(context.Context, domain.ImageCreateRequest) (domain.ImageProfile, error) {
	return domain.ImageProfile{}, nil
}

func TestCatalogDistinguishesConfiguredRecipeFromObservedVM(t *testing.T) {
	c, builder := imageController(t)
	profile := *c.templates["test"].Image
	profile.MacOS = "12.6"
	c.templates["macos-monterey-golden"] = domain.Template{ID: "macos-monterey-golden", Name: "monterey-golden", Location: "home", Image: &profile}
	delete(c.templates, "test")
	c.SetImageCatalog(statusCatalog{}, []string{"home"})
	read := func() domain.ImageInventory {
		t.Helper()
		catalog, err := c.ImageCatalog(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return catalog.Inventory
	}
	if got := read(); !got.Verified || len(got.VMs) != 0 || len(got.ConfiguredImages) != 1 || got.ConfiguredImages[0].Present || got.ConfiguredImages[0].Ready {
		t.Fatal("configured Monterey recipe was mistaken for an existing VM", got)
	}
	builder.backend.mu.Lock()
	builder.backend.vms["home/monterey-golden"] = domain.VM{Name: "monterey-golden", Location: "home", State: "stopped", IP: "192.0.2.1", SSHAvailable: true}
	builder.backend.mu.Unlock()
	tick(t, c)
	got := read()
	if !got.Verified || len(got.VMs) != 1 || !got.ConfiguredImages[0].Present || got.ConfiguredImages[0].Ready {
		t.Fatal("observed but unbuilt VM state is wrong", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), "192.0.2.1") || strings.Contains(string(encoded), "ssh_available") {
		t.Fatal("inventory leaked host connection details", string(encoded), err)
	}
	c.mu.Lock()
	c.lastObservedMono = c.lastObservedMono.Add(-11 * time.Second)
	c.mu.Unlock()
	if read().Verified {
		t.Fatal("stale inventory was marked verified")
	}
}

func TestCatalogReportsLocalXcodeStateWithoutMutatingSource(t *testing.T) {
	c, builder := imageController(t)
	x := domain.XcodeRelease{Version: "13.4.1", Build: "13F100", Requires: "12.0", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip", SHA1: strings.Repeat("a", 40)}
	source := domain.ImageCatalog{Xcode: []domain.XcodeRelease{x}}
	c.SetImageCatalog(statusCatalog{source}, []string{"home"})
	read := func() domain.XcodeRelease {
		t.Helper()
		v, err := c.ImageCatalog(context.Background())
		if err != nil || len(v.Xcode) != 1 {
			t.Fatal(v, err)
		}
		return v.Xcode[0]
	}
	if got := read(); got.LocalState != "available" {
		t.Fatal(got)
	}
	builder.cached = true
	if got := read(); got.LocalState != "downloaded" {
		t.Fatal(got)
	}
	p := *c.templates["test"].Image
	p.MacOS, p.Build, p.Xcode, p.Provision = "12.6", "21G115", &x, "developer-v1"
	template := c.templates["test"]
	template.Image = &p
	c.templates["test"] = template
	if _, err := c.BuildImage(context.Background(), "test", "catalog-status-build"); err != nil {
		t.Fatal(err)
	}
	for range 9 {
		tick(t, c)
	}
	if got := read(); got.LocalState != "installed" || len(got.InstalledImages) != 1 || got.InstalledImages[0] != "test" {
		t.Fatal(got)
	}
	changed := c.templates["test"]
	copyProfile := *changed.Image
	copyProfile.Provision = "security-v1"
	changed.Image = &copyProfile
	c.templates["test"] = changed
	if got := read(); got.LocalState != "downloaded" || len(got.InstalledImages) != 0 {
		t.Fatal("stale image manifest reported installed", got)
	}
	if source.Xcode[0].LocalState != "" || len(source.Xcode[0].InstalledImages) != 0 {
		t.Fatal("catalog metadata was modified in place")
	}
}

type fakeCatalog struct {
	p     domain.ImageProfile
	calls int
	fail  bool
}

func (f *fakeCatalog) List(context.Context) (domain.ImageCatalog, error) {
	return domain.ImageCatalog{}, nil
}
func (f *fakeCatalog) Resolve(context.Context, domain.ImageCreateRequest) (domain.ImageProfile, error) {
	f.calls++
	if f.fail {
		return f.p, domain.Err("catalog_unavailable", "offline")
	}
	return f.p, nil
}
func TestCatalogImagePersistsAndReplaysWithoutNetwork(t *testing.T) {
	c, b := imageController(t)
	ctx := context.Background()
	cat := &fakeCatalog{p: *c.templates["test"].Image}
	cat.p.Build = "21G115"
	cat.p.MacOS = "12.6"
	c.SetImageCatalog(cat, []string{"home"})
	r := domain.ImageCreateRequest{ID: "monterey", MacOS: "monterey"}
	first, err := c.CreateImage(ctx, r, "create-monterey-one")
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := c.store.Templates(ctx)
	if err != nil || len(persisted) != 1 || persisted[0].Image.Build != "21G115" {
		t.Fatal(persisted, err)
	}
	for range 8 {
		tick(t, c)
	}
	restarted, err := New(c.store, b.backend, nil, 2, c.log)
	if err != nil {
		t.Fatal(err)
	}
	cat.fail = true
	restarted.SetImageCatalog(cat, []string{"home"})
	restarted.SetImageBuilder(b)
	replay, err := restarted.CreateImage(ctx, r, "create-monterey-one")
	if err != nil || !replay.Replayed || replay.Job.ID != first.Job.ID || cat.calls != 1 {
		t.Fatal(replay, err, cat.calls)
	}
	r.Security = "automation"
	_, err = restarted.CreateImage(ctx, r, "create-monterey-one")
	code(t, err, "idempotency_conflict")
	r.Security = ""
	r.Xcode = "13.4.1"
	_, err = restarted.CreateImage(ctx, r, "create-monterey-one")
	code(t, err, "idempotency_conflict")
	if restarted.templates["monterey"].Image.Build != "21G115" {
		t.Fatal("lost persisted template")
	}
}
func TestRejectedCatalogBuildLeavesNoTemplate(t *testing.T) {
	c, builder := imageController(t)
	ctx := context.Background()
	cat := &fakeCatalog{p: *c.templates["test"].Image}
	c.SetImageCatalog(cat, []string{"home"})
	if _, err := c.BuildImage(ctx, "test", "existing-build-one"); err != nil {
		t.Fatal(err)
	}
	builder.backend.hostExtra = 1
	tick(t, c)
	_, err := c.CreateImage(ctx, domain.ImageCreateRequest{ID: "new", MacOS: "12.6"}, "new-image-request")
	code(t, err, "capacity_exhausted")
	ts, err := c.store.Templates(ctx)
	if err != nil || len(ts) != 0 {
		t.Fatal(ts, err)
	}
	if _, ok := c.templates["new"]; ok {
		t.Fatal("rejected request reserved template")
	}
	_, err = c.CreateImage(ctx, domain.ImageCreateRequest{ID: "new", MacOS: "12.6", Location: "unconfigured"}, "other-image-request")
	code(t, err, "invalid_request")
}

// Resolution can overlap, but admission plus durable registration is serialized.
type concurrentCatalog struct{ p domain.ImageProfile }

func (f concurrentCatalog) List(context.Context) (domain.ImageCatalog, error) {
	return domain.ImageCatalog{}, nil
}
func (f concurrentCatalog) Resolve(context.Context, domain.ImageCreateRequest) (domain.ImageProfile, error) {
	return f.p, nil
}
func TestConcurrentCatalogAdmission(t *testing.T) {
	c, _ := imageController(t)
	c.SetImageCatalog(concurrentCatalog{*c.templates["test"].Image}, []string{"home"})
	var wg sync.WaitGroup
	ops := make(chan domain.Operation, 8)
	for range 8 {
		wg.Go(func() {
			op, err := c.CreateImage(context.Background(), domain.ImageCreateRequest{ID: "new", MacOS: "12.6"}, "same-catalog-request")
			if err != nil {
				t.Error(err)
			}
			ops <- op
		})
	}
	wg.Wait()
	close(ops)
	id := ""
	fresh := 0
	for op := range ops {
		if id == "" {
			id = op.Job.ID
		}
		if op.Job.ID != id {
			t.Fatal("duplicate job")
		}
		if !op.Replayed {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatal(fresh)
	}
}
