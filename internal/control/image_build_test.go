package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	backend  *fakeBackend
	steps    []string
	fail     string
	failCode string
	cached   bool
}

type shutdownImageBuilder struct {
	started chan struct{}
	check   chan struct{}
	seen    chan error
	release chan struct{}
}

func (b *shutdownImageBuilder) Step(ctx context.Context, _ domain.Lease, _ domain.ImageProfile, _ string, progress func(string) error) error {
	close(b.started)
	<-b.check
	b.seen <- ctx.Err()
	<-b.release
	return progress("stage completed during shutdown")
}

func TestGracefulShutdownKeepsImageStageAliveUntilConfirmed(t *testing.T) {
	c, _ := imageController(t)
	b := &shutdownImageBuilder{started: make(chan struct{}), check: make(chan struct{}), seen: make(chan error, 1), release: make(chan struct{})}
	c.SetImageBuilder(b)
	op, err := c.BuildImage(context.Background(), "test", "shutdown-image")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	if err := c.Tick(runCtx); err != nil {
		cancel()
		t.Fatal(err)
	}
	select {
	case <-b.started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("image stage did not start")
	}
	cancel()
	close(b.check)
	select {
	case err := <-b.seen:
		if err != nil {
			t.Fatal("shutdown canceled an in-flight image stage", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("image stage did not check its operation context")
	}
	close(b.release)
	c.wg.Wait()
	j, err := c.Job(context.Background(), op.Job.ID)
	if err != nil || j.Phase != "download_done" || j.State != "running" {
		t.Fatal("completed stage was not persisted", j, err)
	}
}

func (b *fakeImageBuilder) CachedXcode(domain.XcodeRelease) bool { return b.cached }

func (b *fakeImageBuilder) Step(_ context.Context, l domain.Lease, _ domain.ImageProfile, step string, progress func(string) error) error {
	b.steps = append(b.steps, step)
	if step == b.fail {
		code := b.failCode
		if code == "" {
			code = "injected_failure"
		}
		return domain.Err(code, "interrupted stage")
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

func TestImageBuildPersistsLegacyUUID(t *testing.T) {
	c, _ := imageController(t)
	legacyUUID := "123e4567-e89b-12d3-a456-426614174000"
	tm := c.templates["test"]
	tm.LegacyUUID = legacyUUID
	c.templates["test"] = tm
	op, err := c.BuildImage(context.Background(), "test", "legacy-image-build")
	if err != nil {
		t.Fatal(err)
	}
	if op.Lease.LegacyUUID != legacyUUID {
		t.Fatalf("image build lease lost legacy UUID: %#v", op.Lease)
	}
}

func TestImageBuildReservesOnlyItsOwnSlot(t *testing.T) {
	c, builder := imageController(t)
	c.templates["other"] = domain.Template{ID: "other", Name: "other-golden", Location: "home"}
	builder.backend.mu.Lock()
	builder.backend.vms["home/other-golden"] = domain.VM{Name: "other-golden", Location: "home", OS: "macOS", State: "stopped"}
	builder.backend.mu.Unlock()
	tick(t, c)
	image, err := c.BuildImage(context.Background(), "test", "build-one-slot")
	if err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(context.Background())
	if err != nil || status.Capacity.Used != 1 || status.Capacity.Available != 1 {
		t.Fatal(status.Capacity, err)
	}
	if len(status.ImageReservations) != 1 || status.ImageReservations[0].ImageID != image.Lease.ID || status.ImageReservations[0].JobID != image.Job.ID || status.ImageReservations[0].Deadline.IsZero() {
		t.Fatal("image reservation missing from status", status.ImageReservations)
	}
	worker, err := c.Acquire(context.Background(), "other-during-image", domain.AcquireRequest{Template: "other", TTLSeconds: 3600})
	if err != nil || worker.Lease.ID == "" {
		t.Fatal(worker, err)
	}
	_, err = c.Acquire(context.Background(), "same-during-image", domain.AcquireRequest{Template: "test", TTLSeconds: 3600})
	code(t, err, "image_in_use")
	_ = image
}
func TestImageBuildStartsWithOtherReadyLease(t *testing.T) {
	c, builder := imageController(t)
	c.templates["other"] = domain.Template{ID: "other", Name: "other-golden", Location: "home"}
	builder.backend.mu.Lock()
	builder.backend.vms["home/other-golden"] = domain.VM{Name: "other-golden", Location: "home", OS: "macOS", State: "stopped"}
	builder.backend.mu.Unlock()
	tick(t, c)
	worker, err := c.Acquire(context.Background(), "other-before-image", domain.AcquireRequest{Template: "other", TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, c, worker)
	if _, err := c.BuildImage(context.Background(), "test", "build-alongside-worker"); err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(context.Background())
	if err != nil || status.Capacity.Used != 2 || status.Capacity.Available != 0 {
		t.Fatal(status.Capacity, err)
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

func TestImageRecoveryAdoptsLegacyUUID(t *testing.T) {
	c, b := imageController(t)
	ctx := context.Background()
	legacyUUID := "123e4567-e89b-12d3-a456-426614174000"
	tm := c.templates["test"]
	tm.LegacyUUID = legacyUUID
	c.templates["test"] = tm
	op, err := c.BuildImage(ctx, "test", "legacy-recovery-build")
	if err != nil {
		t.Fatal(err)
	}
	l := op.Lease
	l.State = "needs_attention"
	l.UpdatedAt = c.now().Add(-time.Minute)
	j := op.Job
	j.State = "needs_attention"
	j.Phase = "setup_dispatched"
	j.Image.MacOS = "12.6"
	j.UpdatedAt = l.UpdatedAt
	if err := c.store.Save(ctx, l, &j, "", "", "test.legacy_recovery", "fixture"); err != nil {
		t.Fatal(err)
	}
	b.backend.mu.Lock()
	b.backend.vms[l.Key()] = domain.VM{Name: l.VMName, Location: l.Location, OS: "macOS", State: "stopped"}
	b.backend.mu.Unlock()
	tick(t, c)
	recovered, err := c.RecoverImage(ctx, l.ID, "legacy-recovery-key", l.VMName, "setup-online", true)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Lease.LegacyUUID != legacyUUID {
		t.Fatalf("recovery did not adopt legacy UUID: %#v", recovered.Lease)
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

func TestOnlineSetupRecoveryDoesNotRepeatCreate(t *testing.T) {
	c, b := imageController(t)
	p := c.templates["test"].Image
	p.MacOS = "12.6"
	p.Build = "21G115"
	b.fail = "setup"
	op, err := c.BuildImage(context.Background(), "test", "online-setup-build")
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		tick(t, c)
	}
	_, err = c.RecoverImage(context.Background(), op.Lease.ID, "ordinary-setup-retry", "golden", "retry", true)
	code(t, err, "unsafe_retry")
	b.fail = ""
	recovery, err := c.RecoverImage(context.Background(), op.Lease.ID, "online-setup-recovery", "golden", "setup-online", true)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Job.Phase != "create_done" {
		t.Fatal(recovery.Job.Phase)
	}
	for range 7 {
		tick(t, c)
	}
	got, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || got.State != "image_ready" {
		t.Fatal(got, err)
	}
	creates := 0
	for _, stage := range b.steps {
		if stage == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatal("replayed restore", b.steps)
	}
}
