package control

import (
	"context"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// ImageBuilder executes only versioned, fixed image preparation stages. Progress
// contains safe text, never process output or credentials.
type ImageBuilder interface {
	Step(context.Context, domain.Lease, domain.ImageProfile, string, func(string) error) error
}

func (c *Controller) SetImageBuilder(b ImageBuilder) { c.imageBuilder = b }

func (c *Controller) BuildImage(ctx context.Context, id, key string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fp := fingerprint([]string{"image_build", id})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	t, ok := c.templates[id]
	if !ok || t.Image == nil || c.imageBuilder == nil {
		return domain.Operation{}, domain.Err("invalid_profile", "image build profile is not configured")
	}
	return c.buildImageLocked(ctx, t, key, fp, false)
}
func (c *Controller) buildImageLocked(ctx context.Context, t domain.Template, key, fp string, register bool) (domain.Operation, error) {
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	if c.capacity(ls).Used != 0 {
		return domain.Operation{}, domain.Err("image_in_use", "image builds require all leases released and all VMs stopped")
	}
	for _, l := range ls {
		if l.Key() == t.Location+"/"+t.Name {
			return domain.Operation{}, domain.Err("image_in_use", "image record already exists; delete it explicitly before rebuilding")
		}
	}
	for _, v := range c.observation.VMs {
		if v.Key() == t.Location+"/"+t.Name {
			return domain.Operation{}, domain.Err("image_exists", "image already exists; build never overwrites a VM")
		}
	}
	now := c.now()
	l := domain.Lease{ID: domain.NewID("image-"), Purpose: "image", Template: t.ID, VMName: t.Name, Location: t.Location, State: "image_building", CreatedAt: now, UpdatedAt: now}
	profile := *t.Image
	j := domain.Job{ID: domain.NewID("job-"), LeaseID: l.ID, Kind: "image_build", Image: &profile, Phase: "queued", State: "queued", CreatedAt: now, UpdatedAt: now, Deadline: now.Add(6 * time.Hour)}
	save := func() error {
		return c.store.Save(ctx, l, &j, key, fp, "image.build_accepted", "Image build accepted; installation and verification are journaled")
	}
	if register {
		save = func() error {
			return c.store.SaveImage(ctx, l, &j, key, fp, "image.build_accepted", "Catalog image accepted; resolved versions are pinned", t)
		}
	}
	if err := save(); err != nil {
		return domain.Operation{}, err
	}
	if register {
		c.templates[t.ID] = t
	}
	return domain.Operation{Lease: l, Job: j}, nil
}

var imageNext = map[string]string{"queued": "download", "download_dispatched": "download", "download_done": "create", "create_done": "setup", "setup_done": "assistant", "assistant_done": "sip", "sip_done": "verify", "verify_done": "stop", "provision_done": "verify"}

func (c *Controller) advanceImageBuild(ctx context.Context, l domain.Lease, j domain.Job) error {
	if j.Image == nil || c.imageBuilder == nil {
		return c.attention(ctx, l, j, "invalid_profile", "Image executor or persisted profile is missing", true)
	}
	if j.Phase == "stop_done" {
		v, exists := c.vm(l)
		if !exists || v.State != "stopped" {
			return nil
		}
		l.State = "image_ready"
		l.ImageManifest = fingerprint(j.Image)
		l.Error = nil
		j.State = "succeeded"
		j.Phase = "done"
		j.Error = nil
		return c.save(ctx, l, j, "image.ready", "Verified image is stopped and available for cloning")
	}
	step, ok := imageNext[j.Phase]
	if !ok {
		return nil
	}
	if j.Phase == "sip_done" && j.Image.Provision != "" {
		step = "provision"
	}
	if step == "create" {
		if _, exists := c.vm(l); exists {
			return c.attention(ctx, l, j, "name_collision", "Image destination appeared before create; refusing to overwrite it", true)
		}
	}
	j.Phase = step + "_dispatched"
	j.State = "running"
	j.Progress = map[string]string{"download": "Downloading and verifying Apple restore image", "create": "Installing macOS from the verified IPSW", "setup": "Preparing guest account, SSH and automatic login", "assistant": "Completing Setup Assistant and checking Finder", "sip": "Applying guest SIP policy through paired Recovery", "provision": "Installing Xcode and versioned UI automation tools", "verify": "Verifying build, SIP, SSH credentials and desktop after reboot", "stop": "Stopping the verified image before publication"}[step]
	if err := c.save(ctx, l, j, "image.step_started", step); err != nil {
		return err
	}
	c.active[l.ID] = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		opCtx, cancel := context.WithDeadline(ctx, j.Deadline)
		defer cancel()
		progress := func(message string) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			j.Progress = message
			return c.save(opCtx, l, j, "image.progress", message)
		}
		err := c.imageBuilder.Step(opCtx, l, *j.Image, step, progress)
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.active, l.ID)
		saveCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err != nil {
			if step == "download" && opCtx.Err() != nil {
				j.Error = nil
				j.Progress = "Download paused; verified range resume on daemon restart"
				if e := c.save(saveCtx, l, j, "image.download_paused", j.Progress); e != nil {
					c.log.Error("persist paused download", "error", e)
				}
				return
			}
			// Adapter errors are safe typed messages. Never publish raw subprocess output.
			code, msg := "image_step_failed", "Image step "+step+" failed; inspect private stage logs. No mutation will be replayed."
			if e, ok := err.(*domain.Error); ok {
				code = e.Code
				msg = e.Message
			}
			if e := c.attention(saveCtx, l, j, code, msg, step != "download"); e != nil {
				c.log.Error("persist image failure", "error", e)
			}
			return
		}
		j.Phase = step + "_done"
		j.Progress = step + " completed"
		if e := c.save(saveCtx, l, j, "image.step_completed", j.Progress); e != nil {
			c.log.Error("persist image result", "error", e)
		}
	}()
	return nil
}

// RecoverImage is operator-only. The exact persisted stage and VM must be
// inspected; it cannot skip stages or promote an unverified image. Create/setup
// interruptions require deletion and a new build, not a blind rerun.
func (c *Controller) RecoverImage(ctx context.Context, id, key, name, action string, confirmed bool) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fp := fingerprint([]string{"image_recover", id, name, action})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if !confirmed {
		return domain.Operation{}, domain.Err("invalid_request", "inspect the exact VM and confirm no image operation remains in flight")
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	l, err := c.store.Lease(ctx, id)
	if err != nil {
		return domain.Operation{}, err
	}
	if l.Purpose != "image" || l.VMName != name || (l.State != "quarantined" && l.State != "needs_attention") || c.active[id] || !c.observation.At.After(l.UpdatedAt) {
		return domain.Operation{}, domain.Err("operation_in_progress", "image must need attention, have no active operation, and have a fresh inventory after its last transition")
	}
	jobs, err := c.store.Jobs(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	var j domain.Job
	for _, candidate := range jobs {
		if candidate.LeaseID == id && candidate.State == "needs_attention" {
			j = candidate
		}
	}
	if j.ID == "" {
		return domain.Operation{}, domain.Err("not_found", "no failed image job found")
	}
	switch action {
	case "retry", "reprovision":
		previous := map[string]string{"download_dispatched": "queued", "assistant_dispatched": "setup_done", "sip_dispatched": "assistant_done", "verify_dispatched": "provision_done", "provision_dispatched": "sip_done"}
		phase, ok := previous[j.Phase]
		if !ok || j.Kind != "image_build" {
			return domain.Operation{}, domain.Err("unsafe_retry", "this interrupted stage cannot be retried; inspect and delete the incomplete image")
		}
		if action == "reprovision" {
			if j.Phase != "verify_dispatched" || j.Image == nil || j.Image.Provision == "" {
				return domain.Operation{}, domain.Err("unsafe_retry", "Reprovision requires a failed verification of a configured UI-test image")
			}
			phase = "sip_done"
		}
		j.Phase = phase
		j.State = "queued"
		j.Error = nil
		j.Deadline = c.now().Add(6 * time.Hour)
		j.UpdatedAt = c.now()
		l.State = "image_building"
		l.Error = nil
		l.UpdatedAt = c.now()
	case "delete":
		j = domain.Job{ID: domain.NewID("job-"), LeaseID: l.ID, Kind: "image_delete", Phase: "queued", State: "queued", CreatedAt: c.now(), UpdatedAt: c.now(), Deadline: c.now().Add(5 * time.Minute)}
		l.State = "image_deleting"
		l.Error = nil
		l.UpdatedAt = c.now()
	default:
		return domain.Operation{}, domain.Err("invalid_request", "recovery action must be retry, reprovision or delete")
	}
	if err := c.store.Save(ctx, l, &j, key, fp, "image.operator_recovery", "Operator inspected exact image and authorized "+action); err != nil {
		return domain.Operation{}, err
	}
	return domain.Operation{Lease: l, Job: j}, nil
}

// ProvisionImage upgrades an existing verified base under exclusive maintenance.
// Publication is revoked until provisioning and all reboot probes succeed.
func (c *Controller) ProvisionImage(ctx context.Context, id, key, name string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fp := fingerprint([]string{"image_provision", id, name})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	t, ok := c.templates[id]
	if !ok || t.Image == nil || t.Image.Provision == "" || t.Name != name || c.imageBuilder == nil {
		return domain.Operation{}, domain.Err("invalid_profile", "Confirm the exact configured image with a provisioning recipe")
	}
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	if c.capacity(ls).Used != 0 {
		return domain.Operation{}, domain.Err("image_in_use", "Release all leases before provisioning an image")
	}
	for _, l := range ls {
		if l.Purpose == "image" && l.Key() == t.Location+"/"+t.Name && l.State == "image_ready" {
			v, exists := c.vm(l)
			if !exists || v.State != "stopped" {
				break
			}
			now := c.now()
			p := *t.Image
			l.State = "image_building"
			l.UpdatedAt = now
			j := domain.Job{ID: domain.NewID("job-"), LeaseID: l.ID, Kind: "image_build", Image: &p, Phase: "sip_done", State: "queued", CreatedAt: now, UpdatedAt: now, Deadline: now.Add(3 * time.Hour)}
			if err := c.store.Save(ctx, l, &j, key, fp, "image.provision_accepted", "Exclusive image provisioning accepted; publication revoked until verification"); err != nil {
				return domain.Operation{}, err
			}
			return domain.Operation{Lease: l, Job: j}, nil
		}
	}
	return domain.Operation{}, domain.Err("template_unavailable", "A stopped verified base image is required")
}
