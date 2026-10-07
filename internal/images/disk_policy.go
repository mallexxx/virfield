package images

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/qualify"
)

// verifyDiskPolicy logs parsed evidence before enforcing the Lume policy.
func (e *Engine) verifyDiskPolicy(ctx context.Context, l domain.Lease, g *guest) (qualify.DiskEvidence, error) {
	evidence, err := qualify.DiskPolicy(ctx, g, l.LegacyUUID != "")
	var failure *domain.Error
	if errors.As(err, &failure) && failure.Code == "disk_policy_unknown" {
		return evidence, err
	}
	encoded, marshalErr := json.Marshal(evidence)
	if marshalErr != nil {
		return evidence, marshalErr
	}
	if logErr := e.provisionLog(l, "disk-policy", string(encoded)); logErr != nil {
		return evidence, logErr
	}
	return evidence, err
}
