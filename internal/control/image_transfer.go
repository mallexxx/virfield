package control

import (
	"context"
	"errors"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type ImageTransferer interface {
	TransferImage(context.Context, domain.Lease, domain.Lease) (string, error)
}

func (c *Controller) SetImageTransferer(t ImageTransferer) { c.imageTransferer = t }

func (c *Controller) TransferImage(ctx context.Context, r domain.ImageTransferRequest, key string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	if err := r.Validate(); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fp := fingerprint(struct {
		Kind    string
		Request domain.ImageTransferRequest
	}{"image_transfer", r})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	t, ok := c.templates[r.Template]
	if !ok || t.Image == nil || t.Image.Tart == nil || t.Source != "" || c.secondaryProvider(t.Provider) || c.imageTransferer == nil {
		return domain.Operation{}, domain.Err("invalid_profile", "transfer requires a verified managed local Tart golden")
	}
	if !c.imageLocations[r.Location] || r.Location == t.Location {
		return domain.Operation{}, domain.Err("invalid_request", "select a different configured local Tart storage")
	}
	if _, exists := c.templates[r.ID]; exists {
		return domain.Operation{}, domain.Err("image_exists", "destination template ID already exists")
	}
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	// Exclusive maintenance also protects aliases of identity-preserving images.
	if c.capacity(ls).Used != 0 {
		return domain.Operation{}, domain.Err("image_in_use", "transfer requires an idle pool")
	}
	var source domain.Lease
	for _, l := range ls {
		if l.Template == r.ID || l.Key() == r.Location+"/"+r.ID {
			return domain.Operation{}, domain.Err("image_exists", "destination is already reserved")
		}
		if l.Purpose == "image" && l.Key() == t.Location+"/"+t.Name && l.State == "image_ready" && l.ImageManifest == imageFingerprint(t.Image) {
			source = l
		}
	}
	if source.ID == "" {
		return domain.Operation{}, domain.Err("template_unavailable", "source has no matching verified image record")
	}
	var resources domain.Resources
	found := false
	for _, v := range c.observation.VMs {
		if v.Key() == r.Location+"/"+r.ID {
			return domain.Operation{}, domain.Err("image_exists", "destination VM already exists")
		}
		if v.Key() == source.Key() {
			if v.State != "stopped" {
				return domain.Operation{}, domain.Err("image_in_use", "source must be stopped")
			}
			resources, found = v.Resources, true
		}
	}
	if !found {
		return domain.Operation{}, domain.Err("template_unavailable", "source VM is absent")
	}
	for _, v := range c.templates {
		if v.Location == r.Location && v.Name == r.ID {
			return domain.Operation{}, domain.Err("image_exists", "destination name belongs to another template")
		}
	}
	if c.capacity(ls).Available < 1 {
		return domain.Operation{}, c.capacity(ls).FullError()
	}
	if c.resourceLimits == nil || resources.CPU < 1 || resources.MemoryBytes < 1 || resources.DiskBytes < 1 {
		return domain.Operation{}, domain.Err("resource_unknown", "Local transfer requires verified source resources and a destination disk budget")
	}
	resources, _, err = c.admitResourceBudget(ls, resources, []string{r.Location})
	if err != nil {
		return domain.Operation{}, err
	}
	destination := t
	destination.ID, destination.Name, destination.Location, destination.Resources = r.ID, r.ID, r.Location, resources
	now := c.now()
	l := domain.Lease{ID: domain.NewID("image-"), Purpose: "image", Template: r.ID, VMName: r.ID, Location: r.Location, Provider: t.Provider, GuestUser: t.GuestUser, GuestHome: t.GuestHome, LegacyUUID: t.LegacyUUID, Source: &t, Resources: resources, State: "image_transferring", CreatedAt: now, UpdatedAt: now}
	j := domain.Job{ID: domain.NewID("job-"), LeaseID: l.ID, Kind: "image_transfer", Phase: "queued", State: "queued", Transfer: &domain.ImageTransfer{Source: source, Destination: destination}, CreatedAt: now, UpdatedAt: now, Deadline: now.Add(6 * time.Hour)}
	if err := c.store.Save(ctx, l, &j, key, fp, "image.transfer_accepted", "Verified local copy accepted; source is preserved and destination is unavailable until content verification"); err != nil {
		return domain.Operation{}, err
	}
	return domain.Operation{Lease: l, Job: j}, nil
}

func (c *Controller) advanceImageTransfer(ctx context.Context, l domain.Lease, j domain.Job) error {
	if j.Transfer == nil || c.imageTransferer == nil {
		return c.attention(ctx, l, j, "invalid_profile", "Persisted transfer or executor is missing", true)
	}
	if j.Phase == "copy_done" {
		source, err := c.store.Lease(ctx, j.Transfer.Source.ID)
		if err != nil {
			return err
		}
		if source.State != "image_ready" || source.Key() != j.Transfer.Source.Key() || source.ImageManifest != j.Transfer.Source.ImageManifest {
			return c.attention(ctx, l, j, "image_transfer_mismatch", "Source verification changed before destination registration", true)
		}
		v, exists := c.vm(l)
		if !exists {
			return nil
		}
		if v.State != "stopped" || v.OS != j.Transfer.Destination.OS || v.Resources != l.Resources || j.Transfer.ContentSHA256 == "" {
			return c.attention(ctx, l, j, "image_transfer_mismatch", "Copied runtime identity or resources changed; inspect before cleanup", true)
		}
		l.State, l.ImageManifest, l.Error = "image_ready", j.Transfer.Source.ImageManifest, nil
		// The source reservation ends only with atomic destination registration.
		l.Source = nil
		j.State, j.Phase, j.Error = "succeeded", "done", nil
		l.UpdatedAt, j.UpdatedAt = c.now(), c.now()
		if err := c.store.SaveImage(ctx, l, &j, "", "", "image.transferred", "Copied content, credentials and verification provenance preserved; source unchanged", j.Transfer.Destination); err != nil {
			return err
		}
		c.templates[l.Template] = j.Transfer.Destination
		return nil
	}
	if j.Phase != "queued" {
		return nil
	}
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return err
	}
	other := make([]domain.Lease, 0, len(ls))
	for _, lease := range ls {
		if lease.ID != l.ID {
			other = append(other, lease)
		}
	}
	if _, _, err := c.admitResourceBudget(other, l.Resources, []string{l.Location}); err != nil {
		return c.attention(ctx, l, j, "resource_unavailable", "Destination admission changed before transfer; inspect or delete the unstarted copy", true)
	}
	if vm, exists := c.vm(j.Transfer.Source); !exists || vm.State != "stopped" {
		return c.attention(ctx, l, j, "image_in_use", "Source disappeared or started before transfer; nothing copied", true)
	}
	if _, exists := c.vm(l); exists {
		return c.attention(ctx, l, j, "name_collision", "Destination appeared before transfer; refusing to overwrite", true)
	}
	j.Phase, j.State = "copy_dispatched", "running"
	j.Progress = "Copying local golden and verifying source/destination hashes"
	if err := c.save(ctx, l, j, "image.transfer_started", j.Progress); err != nil {
		return err
	}
	c.active[l.ID] = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		op, cancel := context.WithDeadline(context.WithoutCancel(ctx), j.Deadline)
		defer cancel()
		digest, err := c.imageTransferer.TransferImage(op, j.Transfer.Source, l)
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.active, l.ID)
		save, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err != nil {
			code, msg := "image_transfer_failed", "Local transfer failed; partial copy stays quarantined. Inspect before deleting; copy will not be replayed."
			var fault *domain.Error
			if errors.As(err, &fault) {
				code, msg = fault.Code, fault.Message
			}
			if e := c.attention(save, l, j, code, msg, true); e != nil {
				c.log.Error("persist transfer failure", "error", e)
			}
			return
		}
		j.Transfer.ContentSHA256 = digest
		j.Phase = "copy_done"
		j.Progress = "Local content hashes verified; waiting for fresh stopped runtime inventory"
		if e := c.save(save, l, j, "image.transfer_verified", j.Progress); e != nil {
			c.log.Error("persist transfer result", "error", e)
		}
	}()
	return nil
}
