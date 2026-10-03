package control

import (
	"context"
	"github.com/mallexxx/virfield/internal/domain"
	"sort"
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
	v, err := c.imageCatalog.List(ctx)
	if err != nil {
		return v, err
	}
	v.Xcode = append([]domain.XcodeRelease(nil), v.Xcode...)
	leases, err := c.store.Leases(ctx)
	if err != nil {
		return domain.ImageCatalog{}, err
	}
	ready := make(map[string]string)
	for _, l := range leases {
		if l.Purpose == "image" && l.State == "image_ready" {
			ready[l.Template] = l.ImageManifest
		}
	}
	c.mu.Lock()
	templates := make([]domain.Template, 0, len(c.templates))
	inventory := domain.ImageInventory{Verified: c.healthy() == nil, ObservedAt: c.observation.At, VMs: []domain.InventoryVM{}, ConfiguredImages: []domain.ConfiguredImage{}}
	present := make(map[string]bool, len(c.observation.VMs))
	for _, vm := range c.observation.VMs {
		inventory.VMs = append(inventory.VMs, domain.InventoryVM{Name: vm.Name, Location: vm.Location, OS: vm.OS, State: vm.State})
		present[vm.Key()] = true
	}
	sort.Slice(inventory.VMs, func(i, j int) bool {
		if inventory.VMs[i].Location == inventory.VMs[j].Location {
			return inventory.VMs[i].Name < inventory.VMs[j].Name
		}
		return inventory.VMs[i].Location < inventory.VMs[j].Location
	})
	for _, t := range c.templates {
		if t.Image == nil {
			continue
		}
		item := domain.ConfiguredImage{ID: t.ID, Name: t.Name, Location: t.Location, MacOS: t.Image.MacOS, Present: present[t.Location+"/"+t.Name]}
		if t.Image.Xcode != nil {
			item.Xcode = t.Image.Xcode.Version
		}
		item.Ready = item.Present && ready[t.ID] != "" && ready[t.ID] == imageFingerprint(t.Image)
		inventory.ConfiguredImages = append(inventory.ConfiguredImages, item)
		if item.Ready && t.Image.Xcode != nil {
			templates = append(templates, t)
		}
	}
	sort.Slice(inventory.ConfiguredImages, func(i, j int) bool { return inventory.ConfiguredImages[i].ID < inventory.ConfiguredImages[j].ID })
	builder := c.imageBuilder
	c.mu.Unlock()
	v.Inventory = inventory
	cache, _ := builder.(interface {
		CachedXcode(domain.XcodeRelease) bool
	})
	for i := range v.Xcode {
		x := &v.Xcode[i]
		x.InstalledImages = nil
		x.LocalState = "available"
		if cache != nil && cache.CachedXcode(*x) {
			x.LocalState = "downloaded"
		}
		for _, t := range templates {
			if t.Image.Xcode.Version == x.Version && t.Image.Xcode.Build == x.Build && t.Image.Xcode.SHA1 == x.SHA1 {
				x.InstalledImages = append(x.InstalledImages, t.ID)
				x.LocalState = "installed"
			}
		}
		sort.Strings(x.InstalledImages)
	}
	return v, nil
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
