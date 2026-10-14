package tartimage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

type exportRegistry struct{ uploaded bool }

func (r *exportRegistry) Source(string) (domain.RegistrySource, error) {
	return domain.RegistrySource{ID: "team", Organization: "team", AllowPush: true}, nil
}
func (r *exportRegistry) Resolve(context.Context, domain.RegistryResolveRequest) (domain.RegistryReference, error) {
	if !r.uploaded {
		return domain.RegistryReference{}, domain.Err("registry_not_found", "absent")
	}
	return domain.RegistryReference{Digest: "sha256:" + strings.Repeat("b", 64)}, nil
}

type exportRunner struct {
	fakeRunner
	registry *exportRegistry
	fail     bool
}

func (r *exportRunner) Run(ctx context.Context, dir string, env []string, log, name string, args ...string) error {
	if err := r.fakeRunner.Run(ctx, dir, env, log, name, args...); err != nil {
		return err
	}
	if r.fail {
		return domain.Err("injected_failure", "push failed")
	}
	r.registry.uploaded = true
	return nil
}

func TestExportRequiresPortableSanitizationAndUploadsOnlyRebuiltVM(t *testing.T) {
	b, _, g, l, p := qualifyBuilder(t)
	registry := &exportRegistry{}
	runner := &exportRunner{registry: registry}
	b.run = runner
	b.config.Registry = registry
	l.Source = &domain.Template{Name: "original-private-golden", Image: &p}
	target := domain.RegistryExport{Source: "team", Organization: "team", Repository: "vm", Tag: "new"}
	if _, err := b.ExportStep(context.Background(), l, target, "sanitize"); !hasCode(err, "registry_push_disabled") {
		t.Fatal(err)
	}
	l.Portable = true
	l.VMName = "vf-export-test"
	if _, err := b.ExportStep(context.Background(), l, target, "upload"); !hasCode(err, "registry_sanitize_failed") {
		t.Fatal(err)
	}
	// The runtime fixture observes ventura-golden; verify the sanitizer
	// independently, then supply an exact stopped rebuilt identity for upload.
	l.VMName = "vf-export-test"
	b.config.Runtime = exportRuntime{b.config.Runtime, l}
	if _, err := b.ExportStep(context.Background(), l, target, "sanitize"); err != nil {
		t.Fatal(err)
	}
	if g.secured != 0 {
		t.Fatal("sanitizer installed private image credentials")
	}
	raw, err := os.ReadFile(filepath.Join(b.folder(l), "sanitized-vm"))
	if err != nil || string(raw) != l.Key() {
		t.Fatal(err)
	}
	digest, err := b.ExportStep(context.Background(), l, target, "upload")
	if err != nil || !domain.ValidRegistryDigest(digest) {
		t.Fatal(digest, err)
	}
	if len(runner.calls) != 1 || strings.Join(runner.calls[0].args, " ") != "push vf-export-test ghcr.io/team/vm:new" {
		t.Fatal(runner.calls)
	}
	if strings.Contains(strings.Join(g.commands, "\n"), l.Source.Name) {
		t.Fatal("source golden was mutated")
	}
}

type exportRuntime struct {
	Runtime
	lease domain.Lease
}

func (r exportRuntime) Observe(ctx context.Context) (domain.Observation, error) {
	o, err := r.Runtime.Observe(ctx)
	for i := range o.VMs {
		o.VMs[i].Name = r.lease.VMName
	}
	return o, err
}
