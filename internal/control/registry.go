package control

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
)

type ImageRegistry interface {
	Sources() []map[string]any
	PushTarget(string) (string, error)
	Resolve(context.Context, domain.RegistryResolveRequest) (domain.RegistryReference, error)
}

func (c *Controller) SetRegistry(r ImageRegistry) { c.registry = r }
func (c *Controller) RegistrySources() ([]map[string]any, error) {
	if c.registry == nil {
		return nil, domain.Err("registry_unconfigured", "Configure registry sources on the host")
	}
	return c.registry.Sources(), nil
}
func (c *Controller) ResolveRegistry(ctx context.Context, r domain.RegistryResolveRequest) (domain.RegistryReference, error) {
	if c.registry == nil {
		return domain.RegistryReference{}, domain.Err("registry_unconfigured", "Configure registry sources on the host")
	}
	return c.registry.Resolve(ctx, r)
}
func (c *Controller) PullImage(ctx context.Context, r domain.ImagePullRequest, key string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	if err := r.Validate(); err != nil {
		return domain.Operation{}, err
	}
	fp := fingerprint(struct {
		Kind    string
		Request domain.ImagePullRequest
	}{"image_pull", r})
	c.mu.Lock()
	op, err := c.store.Replay(ctx, key, fp)
	c.mu.Unlock()
	if err != nil {
		return domain.Operation{}, err
	}
	if op != nil {
		return *op, nil
	}
	if c.registry == nil || c.imageCatalog == nil || c.imageBuilder == nil {
		return domain.Operation{}, domain.Err("registry_unconfigured", "Configure registry sources and image tools on the host")
	}
	location := r.Location
	if location == "" {
		location = "home"
	}
	if !c.imageLocations[location] {
		return domain.Operation{}, domain.Err("invalid_request", "Storage location is not configured by the host operator")
	}
	ref, err := c.registry.Resolve(ctx, domain.RegistryResolveRequest{Source: r.Source, Repository: r.Repository, Tag: r.Tag})
	if err != nil {
		return domain.Operation{}, err
	}
	p, err := c.imageCatalog.Resolve(ctx, r.ImageCreateRequest)
	if err != nil {
		return domain.Operation{}, err
	}
	p.Registry = &ref
	p.URL = ""
	p.SHA256 = ""
	p.Size = 0
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
	if _, ok := c.templates[r.ID]; ok {
		return domain.Operation{}, domain.Err("image_exists", "Template ID already exists; choose a new ID")
	}
	for _, t := range c.templates {
		if t.Location == location && t.Name == r.ID {
			return domain.Operation{}, domain.Err("image_exists", "VM name is reserved by another template")
		}
	}
	t := domain.Template{ID: r.ID, Name: r.ID, Location: location, Image: &p}
	return c.buildImageLocked(ctx, t, key, fp, true)
}
