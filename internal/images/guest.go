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
	manager := &guestssh.Manager{Dir: e.Dir}
	if l.Portable {
		// This is a fresh, isolated export build. No private administrator
		// password may ever be written into its disk, even in freed APFS blocks.
		c, err := manager.CredentialsFor(l)
		if err != nil {
			return err
		}
		if c.Secured && c.Password != "lume" {
			return domain.Err("invalid_profile", "Portable build contains private credentials")
		}
		c.Password = "lume"
		if err := guestssh.SaveCredentials(manager.CredentialPath(l), c); err != nil {
			return err
		}
	}
	return manager.SecureImage(ctx, l, ip, g)
}
