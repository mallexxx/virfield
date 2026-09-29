package control

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

type imageCleanupPreparer struct {
	blockingPreparer
	err   error
	calls int
}

func TestImageDeletionProtectsPersistedSourceAfterTemplateRename(t *testing.T) {
	c, _, backend := setup(t)
	op := acquire(t, c, "rename-source-lease")
	ready(t, c, op)
	template := c.templates["test"]
	delete(c.templates, "test")
	template.ID = "renamed"
	c.templates[template.ID] = template
	_, err := c.DeleteImage(context.Background(), template.ID, "renamed-image-delete", template.Name)
	code(t, err, "image_in_use")
	if backend.count("delete") != 0 {
		t.Fatal("deleted source of an active lease")
	}
}

func (p *imageCleanupPreparer) ForgetImage(domain.Lease) error {
	p.calls++
	return p.err
}

func TestImageDeletionWaitsForPrivateDataCleanup(t *testing.T) {
	c, _ := imageController(t)
	ctx := context.Background()
	op, err := c.BuildImage(ctx, "test", "image-cleanup-build")
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		tick(t, c)
	}
	p := &imageCleanupPreparer{err: errors.New("private directory not writable")}
	c.SetLeasePreparer(p)
	if _, err := c.DeleteImage(ctx, "test", "image-cleanup-delete", "golden"); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	if p.calls != 0 {
		t.Fatal("removed credentials before VM absence")
	}
	code(t, c.Tick(ctx), "credential_cleanup_failed")
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State == "released" {
		t.Fatal("released before private cleanup", err)
	}
	p.err = nil
	tick(t, c)
	l, err = c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "released" || p.calls != 2 {
		t.Fatal(l.State, p.calls, err)
	}
}

type fakeImageBuilder struct {
	backend *fakeBackend
	steps   []string
	fail    string
}

func (b *fakeImageBuilder) Step(_ context.Context, l domain.Lease, _ domain.ImageProfile, step string, progress func(string) error) error {
	b.steps = append(b.steps, step)
	if step == b.fail {
		return domain.Err("injected_failure", "interrupted stage")
	}
	if step == "create" {
		b.backend.mu.Lock()
		b.backend.vms[l.Key()] = domain.VM{Name: l.VMName, Location: l.Location, OS: "macOS", State: "stopped"}
		b.backend.mu.Unlock()
	}
	return progress(step + " progress")
}
func imageController(t *testing.T) (*Controller, *fakeImageBuilder) {
	c, _, b := setup(t)
	delete(b.vms, "home/golden")
	p := domain.ImageProfile{URL: "https://updates.cdn-apple.com/test.ipsw", SHA256: strings.Repeat("0", 64), Size: 123, Build: "26A428"}
	c.templates["test"] = domain.Template{ID: "test", Name: "golden", Location: "home", Image: &p}
	builder := &fakeImageBuilder{backend: b}
	c.SetImageBuilder(builder)
	tick(t, c)
	return c, builder
}
func TestImageBuildJournalPromotionAndDelete(t *testing.T) {
	c, b := imageController(t)
	ctx := context.Background()
	op, err := c.BuildImage(ctx, "test", "image-build-first")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := c.BuildImage(ctx, "test", "image-build-first")
	if err != nil || !replay.Replayed || replay.Lease.ID != op.Lease.ID {
		t.Fatal(replay, err)
	}
	_, err = c.BuildImage(ctx, "test", "image-build-second")
	code(t, err, "image_in_use")
	for range 8 {
		tick(t, c)
	}
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "image_ready" {
		t.Fatal(l, err)
	}
	if strings.Join(b.steps, ",") != "download,create,setup,assistant,sip,verify,stop" {
		t.Fatal(b.steps)
	}
	status, err := c.Status(ctx)
	if err != nil || status.Capacity.Used != 0 {
		t.Fatal(status, err)
	}
	deletion, err := c.DeleteImage(ctx, "test", "image-delete-built", "golden")
	if err != nil || deletion.Lease.ID != op.Lease.ID {
		t.Fatal(deletion, err)
	}
	tick(t, c)
	tick(t, c)
	if _, err := c.BuildImage(ctx, "test", "image-build-again"); err != nil {
		t.Fatal(err)
	}
}
func TestImageFailureDoesNotReplayMutation(t *testing.T) {
	c, b := imageController(t)
	b.fail = "setup"
	ctx := context.Background()
	op, err := c.BuildImage(ctx, "test", "image-failure-test")
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		tick(t, c)
	}
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "quarantined" {
		t.Fatal(l, err)
	}
	if err := c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	if strings.Join(b.steps, ",") != "download,create,setup" {
		t.Fatal(b.steps)
	}
	_, err = c.Acquire(ctx, "blocked-image-acquire", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "image_in_use")
}

func TestImageRecoveryRequiresInspectionAndCannotSkipSetup(t *testing.T) {
	c, b := imageController(t)
	b.fail = "setup"
	ctx := context.Background()
	op, err := c.BuildImage(ctx, "test", "image-recovery-setup")
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		tick(t, c)
	}
	_, err = c.RecoverImage(ctx, op.Lease.ID, "image-no-confirm", "golden", "delete", false)
	code(t, err, "invalid_request")
	_, err = c.RecoverImage(ctx, op.Lease.ID, "image-no-retry-setup", "golden", "retry", true)
	code(t, err, "unsafe_retry")
	recovered, err := c.RecoverImage(ctx, op.Lease.ID, "image-delete-failed", "golden", "delete", true)
	if err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	tick(t, c)
	j, err := c.Job(ctx, recovered.Job.ID)
	if err != nil || j.State != "succeeded" {
		t.Fatal(j, err)
	}
	jobs, err := c.store.Jobs(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
}
func TestImageRecoveryRetriesDownloadWithoutChangingIdentity(t *testing.T) {
	c, b := imageController(t)
	b.fail = "download"
	ctx := context.Background()
	op, err := c.BuildImage(ctx, "test", "image-recovery-download")
	if err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	tick(t, c)
	b.fail = ""
	recovered, err := c.RecoverImage(ctx, op.Lease.ID, "image-retry-download", "golden", "retry", true)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Job.ID != op.Job.ID || recovered.Lease.ID != op.Lease.ID {
		t.Fatal("recovery changed ownership identity")
	}
	for range 8 {
		tick(t, c)
	}
	j, err := c.Job(ctx, op.Job.ID)
	if err != nil || j.State != "succeeded" {
		t.Fatal(j, err)
	}
}
func TestImageRestartOnlyQuarantinesUnconfirmedMutations(t *testing.T) {
	for _, phase := range []string{"create_dispatched", "setup_dispatched", "assistant_dispatched", "sip_dispatched", "verify_dispatched", "download_done", "create_done", "setup_done", "assistant_done", "sip_done", "verify_done", "stop_done"} {
		t.Run(phase, func(t *testing.T) {
			c, _ := imageController(t)
			ctx := context.Background()
			op, err := c.BuildImage(ctx, "test", "image-restart-boundary")
			if err != nil {
				t.Fatal(err)
			}
			op.Job.Phase = phase
			op.Job.State = "running"
			if err := c.store.Save(ctx, op.Lease, &op.Job, "", "", "test.crash", phase); err != nil {
				t.Fatal(err)
			}
			if err := c.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			j, err := c.Job(ctx, op.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (j.State == "needs_attention") != strings.HasSuffix(phase, "_dispatched") {
				t.Fatal(j)
			}
		})
	}
}

func TestAcquireRequiresMatchingVerifiedImageManifest(t *testing.T) {
	c, _ := imageController(t)
	ctx := context.Background()
	op, err := c.BuildImage(ctx, "test", "image-verified-manifest")
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		tick(t, c)
	}
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.ImageManifest == "" {
		t.Fatal(l, err)
	}
	// A configuration change must not silently relabel an older SIP/build policy.
	tm := c.templates["test"]
	changed := *tm.Image
	changed.DisableSIP = !changed.DisableSIP
	tm.Image = &changed
	c.templates["test"] = tm
	_, err = c.Acquire(ctx, "image-mismatched-policy", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "template_unavailable")
	changed.DisableSIP = !changed.DisableSIP
	if _, err := c.Acquire(ctx, "image-matching-policy", domain.AcquireRequest{Template: "test", TTLSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionRevokesPublicationAndVerifiesBeforePromotion(t *testing.T) {
	c, _ := imageController(t)
	ctx := context.Background()
	if _, err := c.BuildImage(ctx, "test", "provision-base"); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		tick(t, c)
	}
	template := c.templates["test"]
	profile := *template.Image
	profile.DisableSIP = true
	profile.Provision = "uitest-27-v1"
	template.Image = &profile
	c.templates["test"] = template
	op, err := c.ProvisionImage(ctx, "test", "provision-upgrade", "golden")
	if err != nil {
		t.Fatal(err)
	}
	if op.Lease.State == "image_ready" {
		t.Fatal("image remained published")
	}
	_, err = c.Acquire(ctx, "provision-blocked", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "image_in_use")
	for range 4 {
		tick(t, c)
	}
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State != "image_ready" || l.ImageManifest != fingerprint(&profile) {
		t.Fatal("provisioned image not verified", err)
	}
}

func TestNewGoldenAutomaticallyProvisionsAndFailsClosed(t *testing.T) {
	for _, failure := range []string{"", "provision", "verify"} {
		t.Run("failure="+failure, func(t *testing.T) {
			c, b := imageController(t)
			profile := c.templates["test"].Image
			profile.DisableSIP = true
			profile.Provision = "uitest-27-v1"
			b.fail = failure
			ctx := context.Background()
			op, err := c.BuildImage(ctx, "test", "automatic-uitest-golden")
			if err != nil {
				t.Fatal(err)
			}
			for range 12 {
				tick(t, c)
			}
			l, err := c.Lease(ctx, op.Lease.ID)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "" {
				if l.State != "image_ready" || l.ImageManifest != fingerprint(profile) {
					t.Fatal("complete profile not published", l.State)
				}
				if strings.Join(b.steps, ",") != "download,create,setup,assistant,sip,provision,verify,stop" {
					t.Fatal("new golden skipped its configured setup", b.steps)
				}
			} else {
				if l.State == "image_ready" || l.ImageManifest != "" {
					t.Fatal("failed setup published an image")
				}
				_, err = c.Acquire(ctx, "failed-profile-lease", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
				code(t, err, "image_in_use")
				before := len(b.steps)
				if err := c.Recover(ctx); err != nil {
					t.Fatal(err)
				}
				for range 3 {
					tick(t, c)
				}
				if len(b.steps) != before {
					t.Fatal("failed guest setup was silently replayed")
				}
			}
		})
	}
}

func TestFailedVerificationRequiresExplicitReprovision(t *testing.T) {
	for _, action := range []string{"retry", "reprovision"} {
		t.Run(action, func(t *testing.T) {
			c, b := imageController(t)
			c.templates["test"].Image.DisableSIP = true
			c.templates["test"].Image.Provision = "uitest-27-v1"
			b.fail = "verify"
			ctx := context.Background()
			op, err := c.BuildImage(ctx, "test", "failed-verify-image")
			if err != nil {
				t.Fatal(err)
			}
			for range 12 {
				tick(t, c)
			}
			_, err = c.RecoverImage(ctx, op.Lease.ID, "unconfirmed-reprovision", "golden", action, false)
			code(t, err, "invalid_request")
			b.fail = ""
			recovery, err := c.RecoverImage(ctx, op.Lease.ID, "confirmed-reprovision", "golden", action, true)
			if err != nil {
				t.Fatal(err)
			}
			expected := "provision_done"
			if action == "reprovision" {
				expected = "sip_done"
			}
			if recovery.Job.Phase != expected {
				t.Fatal(recovery.Job.Phase)
			}
			for range 5 {
				tick(t, c)
			}
			l, err := c.Lease(ctx, op.Lease.ID)
			if err != nil || l.State != "image_ready" {
				t.Fatal(l.State, err)
			}
			count := 0
			for _, step := range b.steps {
				if step == "provision" {
					count++
				}
			}
			want := 1
			if action == "reprovision" {
				want = 2
			}
			if count != want {
				t.Fatal("unexpected provisioning replay", count, want)
			}
		})
	}
}
