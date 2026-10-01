package control

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
	"sync"
	"testing"
)

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
	c, _ := imageController(t)
	ctx := context.Background()
	cat := &fakeCatalog{p: *c.templates["test"].Image}
	c.SetImageCatalog(cat, []string{"home"})
	if _, err := c.BuildImage(ctx, "test", "existing-build-one"); err != nil {
		t.Fatal(err)
	}
	_, err := c.CreateImage(ctx, domain.ImageCreateRequest{ID: "new", MacOS: "12.6"}, "new-image-request")
	code(t, err, "image_in_use")
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
