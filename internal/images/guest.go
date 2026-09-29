package images

import (
	"context"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
)

type guest = guestssh.Client

func (e *Engine) connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (*guest, error) {
	return (&guestssh.Manager{Dir: e.Dir}).ConnectReady(ctx, l, ip, bootstrap)
}
func (e *Engine) secure(ctx context.Context, l domain.Lease, ip string, g *guest) error {
	return (&guestssh.Manager{Dir: e.Dir}).SecureImage(ctx, l, ip, g)
}
