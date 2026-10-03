package control

import (
	"context"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type LeasePreparer interface {
	Prepare(context.Context, domain.Lease) (domain.SSHConnection, error)
}

// SetLeasePreparer is configured once before Run; it enables strict SSH readiness.
func (c *Controller) SetLeasePreparer(p LeasePreparer) { c.leasePreparer = p }
func (c *Controller) prepareSSH(ctx context.Context, l domain.Lease, j domain.Job) error {
	if l.SSHPublicKey == "" || l.ImageID == "" {
		return c.attention(ctx, l, j, "ssh_profile_missing", "Lease predates scoped SSH provisioning; release it and acquire a new lease", false)
	}
	j.Phase = "ssh_dispatched"
	j.State = "running"
	j.Progress = "Isolating lease SSH credentials and verifying authentication"
	if err := c.save(ctx, l, j, "lease.ssh_started", j.Progress); err != nil {
		return err
	}
	c.active[l.ID] = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		operation, cancel := context.WithDeadline(context.WithoutCancel(ctx), minDeadline(time.Now().Add(3*time.Minute), j.Deadline))
		defer cancel()
		connection, err := c.leasePreparer.Prepare(operation, l)
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.active, l.ID)
		save, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		fresh, readErr := c.store.Lease(save, l.ID)
		if readErr != nil {
			c.log.Error("read lease SSH result", "error", readErr)
			return
		}
		l = fresh
		if err != nil {
			code, message := "lease_ssh_failed", "Lease SSH isolation did not complete; inspect and release the lease"
			if e, ok := err.(*domain.Error); ok {
				code, message = e.Code, e.Message
			}
			if err := c.attention(save, l, j, code, message, false); err != nil {
				c.log.Error("persist lease SSH failure", "error", err)
			}
			return
		}
		l.SSH = &connection
		j.Phase = "ssh_configured"
		j.Progress = "Lease SSH identity verified"
		if err := c.save(save, l, j, "lease.ssh_verified", j.Progress); err != nil {
			c.log.Error("persist lease SSH result", "error", err)
		}
	}()
	return nil
}

func minDeadline(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
