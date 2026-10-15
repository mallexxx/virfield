package control

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type LeaseRevalidator interface {
	Revalidate(context.Context, domain.Lease) error
}

func (c *Controller) Revalidate(ctx context.Context, id, key string, r domain.RevalidateRequest) (domain.Operation, error) {
	return c.RevalidateAs(ctx, id, key, r, "")
}

func (c *Controller) RevalidateAs(ctx context.Context, id, key string, r domain.RevalidateRequest, owner string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	if !domain.ValidName(id) {
		return domain.Operation{}, domain.Err("invalid_request", "Invalid lease ID")
	}
	if err := r.Validate(); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.store.Lease(ctx, id)
	if err != nil {
		return domain.Operation{}, err
	}
	if owner != "" && l.Owner != owner {
		return domain.Operation{}, domain.Err("forbidden", "Lease belongs to another principal")
	}
	fp := fingerprint(struct {
		Kind, ID, Owner string
		Request         domain.RevalidateRequest
	}{"lease_revalidate", id, owner, r})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	if _, ok := c.leasePreparer.(LeaseRevalidator); !ok {
		return domain.Operation{}, domain.Err("revalidation_unsupported", "This runtime has no read-only lease revalidator")
	}
	if l.Purpose == "image" || l.ImageID == "" || !l.CloneConfirmed || l.SSH == nil || l.Source == nil || l.Source.Image == nil {
		return domain.Operation{}, domain.Err("revalidation_unsupported", "Only a verified owned VM lease can be revalidated")
	}
	if !l.ExpiresAt.After(c.now()) {
		return domain.Operation{}, domain.Err("lease_expired", "Revalidation cannot extend an expired lease")
	}
	if l.State != "needs_attention" || l.Error == nil || l.Error.Code != "vm_drift" || l.StartPending {
		return domain.Operation{}, domain.Err("lease_unavailable", "Only a confirmed drifted lease with no ambiguous operation can be revalidated")
	}
	if c.active[id] {
		return domain.Operation{}, domain.Err("operation_in_progress", "A lease operation is still in flight")
	}
	jobs, err := c.store.Jobs(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	for _, job := range jobs {
		if job.LeaseID == id {
			return domain.Operation{}, domain.Err("operation_in_progress", "An unresolved lease job must finish before revalidation")
		}
	}
	if l.VerifiedRuntime == nil || !l.VerifiedRuntime.Complete() {
		return domain.Operation{}, domain.Err("revalidation_unsupported", "Lease predates a verified runtime identity; preserve the VM for operator inspection")
	}
	if err := domain.VerifyRevalidationProof(l, key, r); err != nil {
		return domain.Operation{}, err
	}
	manifest, err := c.revalidationImage(ctx, l)
	if err != nil {
		return domain.Operation{}, err
	}
	v, exists := c.vm(l)
	if !exists || v.State != "running" || net.ParseIP(v.IP) == nil || !v.SSHAvailable || !c.leaseObservationVerified(l, v, exists) {
		return domain.Operation{}, domain.Err("backend_unavailable", "Revalidation requires a fresh verified running VM and reachable SSH endpoint")
	}
	if !l.VerifiedRuntime.Matches(v) {
		return domain.Operation{}, domain.Err("guest_identity_failed", "Runtime identity differs from this lease; nothing was changed")
	}
	now := c.now()
	deadline := minTime(now.Add(2*time.Minute), l.ExpiresAt)
	job := domain.Job{ID: domain.NewID("job-"), LeaseID: id, Kind: "lease_revalidate", State: "queued", Phase: "queued", CreatedAt: now, UpdatedAt: now, Deadline: deadline, Revalidation: &domain.Revalidation{Candidate: v, Request: r, Key: key, Baseline: *l.VerifiedRuntime, ImageManifest: manifest}}
	l.State = "revalidating"
	l.UpdatedAt = now
	expected := *l.VerifiedRuntime
	observed := runtimeIdentity(v, true, c.observation.At)
	d := domain.Diagnostic{Version: 1, Kind: "lease.drift", Action: "revalidation_accepted", Reason: "caller_authorized", Summary: "Read-only revalidation accepted; VM, credentials, workspace and lease deadline are preserved", At: now, Expected: &expected, Observed: &observed, ObservationAt: c.observation.At, ObservationVerified: true}
	if err := c.store.SaveJobDiagnostic(ctx, l, &job, key, fp, d); err != nil {
		return domain.Operation{}, err
	}
	return domain.Operation{Lease: l, Job: job}, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (c *Controller) revalidationImage(ctx context.Context, l domain.Lease) (string, error) {
	if l.Source == nil || l.Source.Image == nil || l.ImageID == "" {
		return "", domain.Err("guest_identity_failed", "Original verified image provenance is unavailable")
	}
	image, err := c.store.Lease(ctx, l.ImageID)
	manifest := imageFingerprint(l.Source.Image)
	if err != nil || image.Purpose != "image" || image.State != "image_ready" || image.Key() != l.Source.Location+"/"+l.Source.Name || image.ImageManifest != manifest {
		return "", domain.Err("guest_identity_failed", "Original image verification changed; existing VM is preserved")
	}
	return manifest, nil
}

func (c *Controller) failRevalidation(ctx context.Context, l domain.Lease, j domain.Job, err error) error {
	var fault *domain.Error
	if !errors.As(err, &fault) {
		fault = domain.Err("revalidation_failed", "Read-only identity checks did not complete; existing VM is preserved")
	}
	j.State = "failed"
	j.Phase = "revalidation_failed"
	j.Error = fault
	j.UpdatedAt = c.now()
	l.State = "needs_attention"
	l.Error = domain.Err("vm_drift", "Revalidation did not restore access; inspect the job error; VM and workspace are preserved")
	l.UpdatedAt = c.now()
	return c.store.SaveJobDiagnostic(ctx, l, &j, "", "", domain.Diagnostic{Version: 1, Kind: "lease.drift", Action: "revalidation_failed", Reason: fault.Code, Summary: fault.Message, At: c.now(), BackendError: diagnosticError(err)})
}

func (c *Controller) advanceRevalidation(ctx context.Context, l domain.Lease, j domain.Job) error {
	if l.State != "revalidating" {
		return nil
	}
	if !l.ExpiresAt.After(c.now()) || !j.Deadline.After(c.now()) {
		return c.failRevalidation(ctx, l, j, domain.Err("lease_expired", "Original revalidation or lease deadline elapsed; no access was restored"))
	}
	if j.Revalidation == nil {
		return c.failRevalidation(ctx, l, j, domain.Err("guest_identity_failed", "Persisted revalidation evidence is missing"))
	}
	if err := j.Revalidation.Validate(l); err != nil {
		return c.failRevalidation(ctx, l, j, err)
	}
	manifest, err := c.revalidationImage(ctx, l)
	if err != nil || manifest != j.Revalidation.ImageManifest {
		if err == nil {
			err = domain.Err("guest_identity_failed", "Image provenance changed before revalidation")
		}
		return c.failRevalidation(ctx, l, j, err)
	}
	v, exists := c.vm(l)
	if !exists || !c.leaseObservationVerified(l, v, exists) || v.State != "running" || !v.SSHAvailable || !j.Revalidation.Baseline.Matches(v) || v.IP != j.Revalidation.Candidate.IP || v.SSHPort != j.Revalidation.Candidate.SSHPort {
		return c.failRevalidation(ctx, l, j, domain.Err("guest_identity_failed", "Candidate runtime or endpoint changed before revalidation"))
	}
	validator, ok := c.leasePreparer.(LeaseRevalidator)
	if !ok {
		return c.failRevalidation(ctx, l, j, domain.Err("revalidation_unsupported", "Read-only lease revalidator is unavailable"))
	}
	if j.Phase != "queued" {
		return c.failRevalidation(ctx, l, j, domain.Err("guest_identity_failed", "Unexpected revalidation phase; no guest operation was performed"))
	}
	j.Phase = "verify_dispatched"
	j.State = "running"
	j.Progress = "Verifying existing runtime, pinned SSH, management authentication and caller-key acceptance"
	if err := c.save(ctx, l, j, "lease.revalidation_started", j.Progress); err != nil {
		return err
	}
	c.active[l.ID] = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		bounded, cancel := context.WithDeadline(context.WithoutCancel(ctx), j.Deadline)
		candidate := l
		candidate.IP = v.IP
		candidate.SSHPort = v.SSHPort
		result := validator.Revalidate(bounded, candidate)
		cancel()
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.active, l.ID)
		save, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		fresh, err := c.store.Lease(save, l.ID)
		if err != nil {
			c.log.Error("read revalidation lease", "error", err)
			return
		}
		latest, err := c.store.Job(save, j.ID)
		if err != nil {
			c.log.Error("read revalidation job", "error", err)
			return
		}
		if fresh.State != "revalidating" || latest.State != "running" || latest.Phase != "verify_dispatched" {
			return
		}
		if result == nil && (!fresh.ExpiresAt.After(c.now()) || !j.Deadline.After(c.now())) {
			result = domain.Err("lease_expired", "Original lease/revalidation deadline elapsed during identity verification")
		}
		current, present := c.vm(fresh)
		if result == nil && (!present || c.healthy() != nil || !c.leaseObservationVerified(fresh, current, present) || current.State != "running" || !j.Revalidation.Baseline.Matches(current) || current.IP != v.IP || current.SSHPort != v.SSHPort || fresh.SSH == nil || fresh.SSH.HostKey != l.SSH.HostKey || fresh.SSH.ClientKeyFingerprint != l.SSH.ClientKeyFingerprint || fresh.SSHPublicKey != l.SSHPublicKey || fresh.ImageID != l.ImageID) {
			result = domain.Err("guest_identity_failed", "Lease or verified endpoint changed while revalidation was in flight")
		}
		if result == nil {
			manifest, err := c.revalidationImage(save, fresh)
			if err != nil {
				result = err
			} else if manifest != j.Revalidation.ImageManifest {
				result = domain.Err("guest_identity_failed", "Image verification changed while revalidation was in flight")
			}
		}
		if result != nil {
			if err := c.failRevalidation(save, fresh, latest, result); err != nil {
				c.log.Error("persist revalidation failure", "error", err)
			}
			return
		}
		connection := *fresh.SSH
		connection.Port = current.SSHPort
		if connection.Port == 0 {
			connection.Port = 22
		}
		fresh.SSH = &connection
		fresh.IP = current.IP
		fresh.SSHPort = current.SSHPort
		fresh.State = "ready"
		fresh.Error = nil
		fresh.UpdatedAt = c.now()
		identity := runtimeIdentity(current, true, c.observation.At)
		fresh.VerifiedRuntime = &identity
		latest.State = "succeeded"
		latest.Phase = "revalidated"
		latest.Progress = "Existing VM identity and SSH access revalidated; workspace and deadline preserved"
		latest.Error = nil
		latest.UpdatedAt = c.now()
		expected := j.Revalidation.Baseline
		d := domain.Diagnostic{Version: 1, Kind: "lease.drift", Action: "revalidated", Reason: "identity_verified", Summary: latest.Progress, At: c.now(), Expected: &expected, Observed: &identity, ObservationAt: c.observation.At, ObservationVerified: true}
		if err := c.store.SaveJobDiagnostic(save, fresh, &latest, "", "", d); err != nil {
			c.log.Error("persist revalidated lease", "error", err)
			return
		}
		delete(c.readyDrift, l.ID)
	}()
	return nil
}
