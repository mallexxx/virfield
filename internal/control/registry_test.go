package control

import (
	"context"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

type fakeRegistry struct {
	calls   int
	missing bool
	fail    bool
}

func (r *fakeRegistry) Sources() []map[string]any         { return []map[string]any{{"id": "team"}} }
func (r *fakeRegistry) PushTarget(string) (string, error) { return "team", nil }
func (r *fakeRegistry) Resolve(_ context.Context, q domain.RegistryResolveRequest) (domain.RegistryReference, error) {
	r.calls++
	if r.fail {
		return domain.RegistryReference{}, domain.Err("registry_unavailable", "offline")
	}
	if r.missing {
		return domain.RegistryReference{}, domain.Err("registry_not_found", "absent")
	}
	return domain.RegistryReference{Source: q.Source, Organization: "team", Repository: q.Repository, Tag: q.Tag, Digest: "sha256:" + strings.Repeat("a", 64), Size: 1024}, nil
}
func TestRegistryPullPersistsPinAndReplaysOffline(t *testing.T) {
	c, b := imageController(t)
	ctx := context.Background()
	cat := &fakeCatalog{p: *c.templates["test"].Image}
	cat.p.MacOS = "12.6"
	cat.p.Build = "21G115"
	c.SetImageCatalog(cat, []string{"home"})
	r := &fakeRegistry{}
	c.SetRegistry(r)
	req := domain.ImagePullRequest{ImageCreateRequest: domain.ImageCreateRequest{ID: "imported", MacOS: "monterey"}, Source: "team", Repository: "macos", Tag: "stable"}
	op, err := c.PullImage(ctx, req, "registry-pull-once")
	if err != nil {
		t.Fatal(err)
	}
	if op.Job.Image.Registry == nil || op.Job.Image.URL != "" || op.Job.Image.SHA256 != "" || op.Job.Image.Build != "21G115" {
		t.Fatal(op.Job.Image)
	}
	for range 8 {
		tick(t, c)
	}
	restarted, err := New(c.store, b.backend, nil, 2, c.log)
	if err != nil {
		t.Fatal(err)
	}
	r.fail = true
	cat.fail = true
	restarted.SetRegistry(r)
	restarted.SetImageCatalog(cat, []string{"home"})
	restarted.SetImageBuilder(b)
	replay, err := restarted.PullImage(ctx, req, "registry-pull-once")
	if err != nil || !replay.Replayed || replay.Job.ID != op.Job.ID || r.calls != 1 || cat.calls != 1 {
		t.Fatal(replay, err, r.calls, cat.calls)
	}
	if restarted.templates["imported"].Image.Registry.Digest != op.Job.Image.Registry.Digest {
		t.Fatal("pin lost across restart")
	}
	req.Tag = "changed"
	_, err = restarted.PullImage(ctx, req, "registry-pull-once")
	code(t, err, "idempotency_conflict")
}

type fakeExporter struct {
	*fakeImageBuilder
	exports []string
	fail    string
}

func (b *fakeExporter) ExportStep(_ context.Context, l domain.Lease, _ domain.RegistryExport, step string) (string, error) {
	if !l.Portable || l.Source == nil || l.VMName == l.Source.Name {
		return "", domain.Err("invalid_profile", "not a fresh portable VM")
	}
	b.exports = append(b.exports, step)
	if b.fail == step {
		return "", domain.Err("registry_publish_unknown", "ambiguous upload")
	}
	if step == "upload" {
		return "sha256:" + strings.Repeat("b", 64), nil
	}
	return "", nil
}
func publishController(t *testing.T) (*Controller, *fakeExporter, *fakeRegistry) {
	t.Helper()
	c, b := imageController(t)
	c.SetImageCatalog(&fakeCatalog{p: *c.templates["test"].Image}, []string{"home"})
	if _, err := c.BuildImage(context.Background(), "test", "source-build-once"); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		tick(t, c)
	}
	e := &fakeExporter{fakeImageBuilder: b}
	c.SetImageBuilder(e)
	r := &fakeRegistry{missing: true}
	c.SetRegistry(r)
	return c, e, r
}
func TestRegistryPublishCompletesOnlyAfterPortableCleanup(t *testing.T) {
	c, b, r := publishController(t)
	ctx := context.Background()
	request := domain.ImagePublishRequest{Template: "test", Source: "team", Repository: "vm", Tag: "new"}
	op, err := c.PublishImage(ctx, request, "publish-once-key")
	if err != nil {
		t.Fatal(err)
	}
	if !op.Lease.Portable || op.Lease.Source.Name != "golden" || op.Lease.VMName == "golden" || op.Job.Export == nil {
		t.Fatal(op)
	}
	_, err = c.DeleteImage(ctx, "test", "delete-during-export", "golden")
	code(t, err, "image_in_use")
	for range 9 {
		tick(t, c)
	}
	j, err := c.Job(ctx, op.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State == "succeeded" || j.Phase != "registry_cleanup" || !domain.ValidRegistryDigest(j.Export.Digest) {
		t.Fatal(j)
	}
	tick(t, c)
	tick(t, c)
	j, err = c.Job(ctx, op.Job.ID)
	if err != nil || j.State != "succeeded" {
		t.Fatal(j, err)
	}
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "released" {
		t.Fatal(l, err)
	}
	if _, ok := b.backend.vms["home/golden"]; !ok {
		t.Fatal("source was removed")
	}
	if _, ok := b.backend.vms[l.Key()]; ok {
		t.Fatal("portable VM still exists")
	}
	if strings.Join(b.exports, ",") != "sanitize,upload" {
		t.Fatal(b.exports)
	}
	r.fail = true
	replay, err := c.PublishImage(ctx, request, "publish-once-key")
	if err != nil || !replay.Replayed || r.calls != 1 {
		t.Fatal(replay, err, r.calls)
	}
}
func TestRegistryPublishDoesNotReplayAmbiguousUpload(t *testing.T) {
	c, b, _ := publishController(t)
	ctx := context.Background()
	b.fail = "upload"
	op, err := c.PublishImage(ctx, domain.ImagePublishRequest{Template: "test", Source: "team", Repository: "vm", Tag: "new"}, "publish-ambiguous")
	if err != nil {
		t.Fatal(err)
	}
	for range 12 {
		tick(t, c)
	}
	if err := c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	l, _ := c.Lease(ctx, op.Lease.ID)
	if l.State != "quarantined" || strings.Join(b.exports, ",") != "sanitize,upload" {
		t.Fatal(l, b.exports)
	}
	_, err = c.RecoverImage(ctx, l.ID, "retry-ambiguous-upload", l.VMName, "retry", true)
	code(t, err, "unsafe_retry")
	if b.backend.count("delete") != 0 {
		t.Fatal("ambiguous export deleted without inspection")
	}
}
func TestRegistryPublishRequiresAbsentTagAndVerifiedSource(t *testing.T) {
	c, b, r := publishController(t)
	ctx := context.Background()
	q := domain.ImagePublishRequest{Template: "test", Source: "team", Repository: "vm", Tag: "existing"}
	r.missing = false
	_, err := c.PublishImage(ctx, q, "existing-publish")
	code(t, err, "registry_tag_exists")
	r.missing = true
	delete(b.backend.vms, "home/golden")
	tick(t, c)
	_, err = c.PublishImage(ctx, q, "missing-source-publish")
	code(t, err, "template_unavailable")
}

func TestInterruptedExportIsQuarantinedWithoutReplay(t *testing.T) {
	for _, phase := range []string{"sanitize_dispatched", "upload_dispatched"} {
		t.Run(phase, func(t *testing.T) {
			c, b, _ := publishController(t)
			ctx := context.Background()
			op, err := c.PublishImage(ctx, domain.ImagePublishRequest{Template: "test", Source: "team", Repository: "vm", Tag: "new"}, "export-crash-test")
			if err != nil {
				t.Fatal(err)
			}
			op.Job.Phase, op.Job.State = phase, "running"
			if err := c.store.Save(ctx, op.Lease, &op.Job, "", "", "test.crash", "fixture"); err != nil {
				t.Fatal(err)
			}
			if err := c.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			tick(t, c)
			l, _ := c.Lease(ctx, op.Lease.ID)
			j, _ := c.Job(ctx, op.Job.ID)
			if l.State != "quarantined" || j.State != "needs_attention" || len(b.exports) != 0 {
				t.Fatal(l, j, b.exports)
			}
		})
	}
}
