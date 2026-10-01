package control

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
)

type ImageCatalog interface {
	List(context.Context) (domain.ImageCatalog, error)
	Resolve(context.Context, domain.ImageCreateRequest) (domain.ImageProfile, error)
}

func (c *Controller) SetImageCatalog(catalog ImageCatalog, locations []string) {
	c.imageCatalog = catalog
	c.imageLocations = map[string]bool{}
	for _, name := range locations {
		c.imageLocations[name] = true
	}
}
func (c *Controller) ImageCatalog(ctx context.Context) (domain.ImageCatalog, error) {
	if c.imageCatalog == nil {
		return domain.ImageCatalog{}, domain.Err("image_dependency", "Image catalog is not configured")
	}
	return c.imageCatalog.List(ctx)
}
func (c *Controller) CreateImage(ctx context.Context, r domain.ImageCreateRequest, key string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	if err := r.Validate(); err != nil {
		return domain.Operation{}, err
	}
	fp := fingerprint(struct {
		Kind    string
		Request domain.ImageCreateRequest
	}{"image_create", r})
	// Replay BEFORE catalog resolution: an accepted alias must never change on retry.
	c.mu.Lock()
	op, err := c.store.Replay(ctx, key, fp)
	c.mu.Unlock()
	if err != nil {
		return domain.Operation{}, err
	}
	if op != nil {
		return *op, nil
	}
	if c.imageCatalog == nil || c.imageBuilder == nil {
		return domain.Operation{}, domain.Err("image_dependency", "Configure image_tools before building catalog images")
	}
	location := r.Location
	if location == "" {
		location = "home"
	}
	if !c.imageLocations[location] {
		return domain.Operation{}, domain.Err("invalid_request", "Storage location is not configured by the host operator")
	}
	p, err := c.imageCatalog.Resolve(ctx, r)
	if err != nil {
		return domain.Operation{}, err
	}
	if err := p.Validate(); err != nil {
		return domain.Operation{}, err
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
	if _, exists := c.templates[r.ID]; exists {
		return domain.Operation{}, domain.Err("image_exists", "Template ID already exists; choose a new ID or use image_build to rebuild its pinned profile")
	}
	for _, t := range c.templates {
		if t.Location == location && t.Name == r.ID {
			return domain.Operation{}, domain.Err("image_exists", "VM name is already reserved by another template")
		}
	}
	t := domain.Template{ID: r.ID, Name: r.ID, Location: location, Image: &p}
	return c.buildImageLocked(ctx, t, key, fp, true)
}
