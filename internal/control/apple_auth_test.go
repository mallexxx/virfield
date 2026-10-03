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
