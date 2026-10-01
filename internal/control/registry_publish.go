package control

import (
	"context"
	"errors"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func (c *Controller) PublishImage(ctx context.Context, r domain.ImagePublishRequest, key string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	if err := r.Validate(); err != nil {
		return domain.Operation{}, err
	}
	fp := fingerprint(struct {
		Kind    string
		Request domain.ImagePublishRequest
	}{"image_publish", r})
	c.mu.Lock()
	op, err := c.store.Replay(ctx, key, fp)
	t, exists := c.templates[r.Template]
	c.mu.Unlock()
	if err != nil {
		return domain.Operation{}, err
	}
	if op != nil {
		return *op, nil
	}
	if !exists || t.Image == nil {
		return domain.Operation{}, domain.Err("unknown_template", "Publishing requires a registered image recipe")
	}
	if c.registry == nil || c.imageBuilder == nil || c.imageCatalog == nil {
		return domain.Operation{}, domain.Err("registry_unconfigured", "Configure registry sources and image tools")
	}
	organization, err := c.registry.PushTarget(r.Source)
	if err != nil {
		return domain.Operation{}, err
	}
	_, err = c.registry.Resolve(ctx, domain.RegistryResolveRequest{Source: r.Source, Repository: r.Repository, Tag: r.Tag})
	var failure *domain.Error
	if err == nil {
		return domain.Operation{}, domain.Err("registry_tag_exists", "Publishing requires a new explicit tag; existing tags are not overwritten")
	}
	if !errors.As(err, &failure) || failure.Code != "registry_not_found" {
		return domain.Operation{}, err
	}
	p := *t.Image
	if p.Registry != nil {
		selection := domain.ImageCreateRequest{ID: r.Template, MacOS: p.Build, Security: p.Security}
		if p.Xcode != nil {
			selection.Xcode = p.Xcode.Version
		}
		p, err = c.imageCatalog.Resolve(ctx, selection)
		if err != nil {
			return domain.Operation{}, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	if c.capacity(ls).Used != 0 {
		return domain.Operation{}, domain.Err("image_in_use", "Publishing requires an idle pool and reserves exclusive image maintenance")
	}
	sourceReady := false
	for _, l := range ls {
		if l.Purpose == "image" && l.Key() == t.Location+"/"+t.Name && l.State == "image_ready" && l.ImageManifest == fingerprint(t.Image) {
			sourceReady = true
		}
	}
	if !sourceReady {
		return domain.Operation{}, domain.Err("template_unavailable", "Publishing requires a verified source recipe")
	}
	observedStopped := false
	for _, vm := range c.observation.VMs {
		if vm.Key() == t.Location+"/"+t.Name {
			observedStopped = vm.State == "stopped"
		}
	}
	if !observedStopped {
		return domain.Operation{}, domain.Err("template_unavailable", "Verified source VM must still exist and be stopped")
	}
	now := c.now()
	id := domain.NewID("image-")
	l := domain.Lease{ID: id, Purpose: "image", Portable: true, Template: t.ID, VMName: "vf-export-" + id[6:], Location: t.Location, State: "image_building", Source: &t, CreatedAt: now, UpdatedAt: now}
	j := domain.Job{ID: domain.NewID("job-"), LeaseID: id, Kind: "image_build", Image: &p, Export: &domain.RegistryExport{Source: r.Source, Organization: organization, Repository: r.Repository, Tag: r.Tag}, Phase: "queued", State: "queued", Deadline: now.Add(8 * time.Hour), CreatedAt: now, UpdatedAt: now}
	if err := c.store.Save(ctx, l, &j, key, fp, "image.publish_accepted", "Portable rebuild accepted; original VM disk and private credentials will not be uploaded"); err != nil {
		return domain.Operation{}, err
	}
	return domain.Operation{Lease: l, Job: j}, nil
}

type ImageExporter interface {
	ExportStep(context.Context, domain.Lease, domain.RegistryExport, string) (string, error)
}

func (c *Controller) advanceImageExport(ctx context.Context, l domain.Lease, j domain.Job) error {
	if !l.Portable || j.Export == nil {
		return c.attention(ctx, l, j, "invalid_profile", "Invalid portable export state", true)
	}
	if j.Phase == "registry_cleanup" {
		j.Phase = "queued"
		return c.advanceImageDelete(ctx, l, j)
	}
	exporter, ok := c.imageBuilder.(ImageExporter)
	if !ok {
		return c.attention(ctx, l, j, "image_dependency", "Image export executor is unavailable", true)
	}
	stages := map[string]string{"stop_done": "sanitize", "sanitize_done": "upload"}
	step, ok := stages[j.Phase]
	if !ok {
		return nil
	}
	j.Phase = step + "_dispatched"
	j.State = "running"
	j.Progress = "Portable registry image: " + step
	if err := c.save(ctx, l, j, "image.export_started", step); err != nil {
		return err
	}
	c.active[l.ID] = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		opCtx, cancel := context.WithDeadline(ctx, j.Deadline)
		defer cancel()
		digest, err := exporter.ExportStep(opCtx, l, *j.Export, step)
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.active, l.ID)
		saveCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err != nil {
			code, message := "registry_export_failed", "Portable export failed; inspect exact job before recovery"
			var failure *domain.Error
			if errors.As(err, &failure) {
				code, message = failure.Code, failure.Message
			}
			if err := c.attention(saveCtx, l, j, code, message, true); err != nil {
				c.log.Error("persist export failure", "error", err)
			}
			return
		}
		j.Phase = step + "_done"
		if step == "upload" {
			if !domain.ValidRegistryDigest(digest) {
				if e := c.attention(saveCtx, l, j, "registry_publish_unknown", "Upload did not return a verified manifest digest; inspect target before recovery", true); e != nil {
					c.log.Error("persist invalid export digest", "error", e)
				}
				return
			}
			j.Export.Digest = digest
			j.Phase = "registry_cleanup"
			l.State = "image_deleting"
			j.Progress = "Registry digest verified; removing temporary portable build"
		}
		if err := c.save(saveCtx, l, j, "image.export_completed", step); err != nil {
			c.log.Error("persist export completion", "error", err)
		}
	}()
	return nil
}
