package control

import (
	"context"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestAppleSignInOnlyResumesExactFailedDownload(t *testing.T) {
	c, builder := imageController(t)
	x := domain.XcodeRelease{Version: "13.4.1", Build: "13F100", Requires: "12.0", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip", SHA1: strings.Repeat("a", 40)}
	p := *c.templates["test"].Image
	p.MacOS, p.Build, p.Xcode, p.Provision = "12.6", "21G115", &x, "developer-v1"
	template := c.templates["test"]
	template.Image = &p
	c.templates["test"] = template
	builder.fail, builder.failCode = "download", "apple_auth_required"
	op, err := c.BuildImage(context.Background(), "test", "apple-auth-test-build")
	if err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	if got, err := c.AppleAuthJob(context.Background(), op.Job.ID); err != nil || got.Version != x.Version {
		t.Fatal(got, err)
	}
	if err := c.ResumeAppleDownload(context.Background(), "missing"); err == nil {
		t.Fatal("unrelated job resumed")
	}
	builder.fail = ""
	if err := c.ResumeAppleDownload(context.Background(), op.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AppleAuthJob(context.Background(), op.Job.ID); err == nil {
		t.Fatal("sign-in link admitted after recovery")
	}
	for range 9 {
		tick(t, c)
	}
	j, err := c.Job(context.Background(), op.Job.ID)
	if err != nil || j.State != "succeeded" {
		t.Fatal(j, err)
	}
}

func TestTartAppleSignInResumesResolveBeforeAnyVMBuild(t *testing.T) {
	c, b := imageController(t)
	p := *c.templates["test"].Image
	p.URL, p.SHA256, p.Size = "", "", 0
	p.MacOS, p.Build, p.Provision = "13.7.4", "22H420", "developer-v1"
	p.Tart = &domain.TartSource{Upstream: "ghcr.io/team/base@sha256:" + strings.Repeat("a", 64)}
	p.Xcode = &domain.XcodeRelease{Version: "15.2", Build: "15C500b", Requires: "13.5", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_15.2/Xcode_15.2.xip", SHA1: strings.Repeat("a", 40)}
	t1 := c.templates["test"]
	t1.Image = &p
	c.templates["test"] = t1
	b.fail, b.failCode = "resolve", "apple_auth_required"
	op, err := c.BuildImage(context.Background(), "test", "tart-auth-resolve")
	if err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	if _, err = c.AppleAuthJob(context.Background(), op.Job.ID); err != nil {
		t.Fatal(err)
	}
	if err = c.ResumeAppleDownload(context.Background(), op.Job.ID); err != nil {
		t.Fatal(err)
	}
	job, err := c.Job(context.Background(), op.Job.ID)
	if err != nil || job.Phase != "queued" {
		t.Fatal(job, err)
	}
	if strings.Join(b.steps, ",") != "resolve" {
		t.Fatal("sign-in dispatched a VM effect", b.steps)
	}
}
