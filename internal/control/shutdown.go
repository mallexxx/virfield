package control

import (
	"context"
	"time"
)

const shutdownGrace = 10 * time.Minute

// ShutdownTimeout includes the mutation grace, Packer's interrupt wait, VM
// shutdown and the final journal write. The API server must wait this long for
// the controller before closing its database and host lock.
const ShutdownTimeout = shutdownGrace + 5*time.Minute + 10*time.Second

func (c *Controller) mutationGrace() time.Duration {
	if c.shutdownGrace > 0 {
		return c.shutdownGrace
	}
	return shutdownGrace
}

// imageContext permits a confirmed stage to finish during normal shutdown, but
// also cancels its subprocess/SSH operation when the drain grace expires. A job's
// original deadline remains an independent upper bound.
func (c *Controller) imageContext(parent context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithDeadline(context.WithoutCancel(parent), deadline)
	grace := c.mutationGrace()
	stop := context.AfterFunc(parent, func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
			cancel()
		}
	})
	return ctx, func() { stop(); cancel() }
}
