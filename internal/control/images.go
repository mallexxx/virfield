package control

import (
	"context"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// DeleteImage is a separate administrative operation. Lease cleanup can never
// delete a template; here the caller explicitly confirms an allowlisted name.
func (c *Controller) DeleteImage(ctx context.Context, id, key, confirmName string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fp := fingerprint([]string{"image_delete", id, confirmName})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	tm, ok := c.templates[id]
	if !ok {
		return domain.Operation{}, domain.Err("unknown_template", "image must be configured by the host operator")
	}
	if confirmName != tm.Name {
		return domain.Operation{}, domain.Err("invalid_request", "confirm_name must match the exact configured VM name")
	}
	leases, err := c.store.Leases(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	var existing *domain.Lease
	for _, l := range leases {
		if l.Purpose == "image" && l.Key() == tm.Location+"/"+tm.Name && l.State == "image_ready" {
			copy := l
			existing = &copy
		}
		// A template ID can be renamed while durable leases still refer to the
		// old ID. Protect their persisted source VM identity as well.
		usesImage := (l.Purpose != "image" && l.Template == id) || (l.Source != nil && l.Source.Location == tm.Location && l.Source.Name == tm.Name)
		if usesImage || (l.Key() == tm.Location+"/"+tm.Name && l.State != "image_ready") {
			return domain.Operation{}, domain.Err("image_in_use", "image has active leases or an image operation; finish them first")
		}
	}
	found := false
	for _, v := range c.observation.VMs {
		if v.Key() == tm.Location+"/"+tm.Name {
			found = true
			if v.State != "stopped" {
				return domain.Operation{}, domain.Err("image_in_use", "image must be stopped before deletion")
			}
		}
	}
	if !found {
		return domain.Operation{}, domain.Err("not_found", "image is absent from Lume")
	}
	now := c.now()
	l := domain.Lease{ID: domain.NewID("image-"), Purpose: "image", Template: id, VMName: tm.Name, Location: tm.Location, State: "image_deleting", CreatedAt: now, UpdatedAt: now}
	if existing != nil {
		l.ID = existing.ID
		l.CreatedAt = existing.CreatedAt
	}
	j := domain.Job{ID: domain.NewID("job-"), LeaseID: l.ID, Kind: "image_delete", Phase: "queued", State: "queued", CreatedAt: now, UpdatedAt: now, Deadline: now.Add(5 * time.Minute)}
	if err := c.store.Save(ctx, l, &j, key, fp, "image.delete_accepted", "Operator authorized deletion of configured image "+tm.Name); err != nil {
		return domain.Operation{}, err
	}
	return domain.Operation{Lease: l, Job: j}, nil
}
func (c *Controller) advanceImageDelete(ctx context.Context, l domain.Lease, j domain.Job) error {
	v, exists := c.vm(l)
	if !exists {
		if disposer, ok := c.leasePreparer.(interface{ ForgetImage(domain.Lease) error }); ok {
			if err := disposer.ForgetImage(l); err != nil {
				return domain.Err("credential_cleanup_failed", "Image deleted; private image data cleanup must finish before releasing reservation")
			}
		}
		l.State = "released"
		j.State = "succeeded"
		j.Phase = "done"
		l.Error, j.Error = nil, nil
		if j.Export != nil {
			j.Progress = "Published registry manifest verified; temporary portable VM and credentials removed"
		}
		return c.save(ctx, l, j, "image.deleted", "Image absence verified in Lume")
	}
	if j.Phase == "queued" || j.Phase == "image_stop_dispatched" {
		if v.State != "stopped" {
			if j.Phase == "queued" {
				return c.dispatch(ctx, l, j, "image_stop_dispatched", func(ctx context.Context) error { return c.backend.Stop(ctx, l) })
			}
			return nil
		}
		return c.dispatch(ctx, l, j, "image_delete_dispatched", func(ctx context.Context) error { return c.backend.Delete(ctx, l) })
	}
	return nil
}
