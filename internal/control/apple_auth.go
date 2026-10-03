package control

import (
	"context"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// AppleAuthJob admits browser sign-in only for the exact failed Xcode download.
func (c *Controller) AppleAuthJob(ctx context.Context, jobID string) (domain.XcodeRelease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	j, err := c.store.Job(ctx, jobID)
	if err != nil {
		return domain.XcodeRelease{}, err
	}
	if j.Kind != "image_build" || j.Phase != "download_dispatched" || j.State != "needs_attention" || j.Error == nil || j.Error.Code != "apple_auth_required" || j.Image == nil || j.Image.Xcode == nil || c.active[j.LeaseID] {
		return domain.XcodeRelease{}, domain.Err("invalid_request", "job is not waiting for Apple Xcode authorization")
	}
	return *j.Image.Xcode, nil
}

// ResumeAppleDownload is safe because download is the only replayed image stage.
// The browser may authorize a retry; it cannot skip verification or VM mutation.
func (c *Controller) ResumeAppleDownload(ctx context.Context, jobID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	j, err := c.store.Job(ctx, jobID)
	if err != nil {
		return err
	}
	if j.Kind != "image_build" || j.Phase != "download_dispatched" || j.State != "needs_attention" || j.Error == nil || j.Error.Code != "apple_auth_required" || j.Image == nil || j.Image.Xcode == nil || c.active[j.LeaseID] {
		return domain.Err("invalid_request", "job is not waiting for Apple Xcode authorization")
	}
	l, err := c.store.Lease(ctx, j.LeaseID)
	if err != nil {
		return err
	}
	if l.Purpose != "image" || l.State != "needs_attention" {
		return domain.Err("operation_in_progress", "image state changed before Apple authorization completed")
	}
	now := c.now()
	j.Phase, j.State, j.Error = "queued", "queued", nil
	j.Progress = "Apple sign-in completed; retrying verified Xcode download"
	j.Deadline = now.Add(10 * time.Hour)
	l.State, l.Error = "image_building", nil
	return c.save(ctx, l, j, "image.apple_auth_completed", "Apple sign-in completed; download stage queued")
}
