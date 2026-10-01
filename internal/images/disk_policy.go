package images

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// Query both boot volumes: FileVault being off does not imply an unencrypted
// APFS volume. In particular Monterey can report encryption at rest without FV.
const diskPolicyProbe = `set -euo pipefail
for volume in / /System/Volumes/Data; do
 info=$(/usr/sbin/diskutil info -plist "$volume")
 for key in FileVault Encryption Locked; do
  value=$(printf '%s' "$info" | /usr/bin/plutil -extract "$key" raw -o - -)
  case "$value" in true|false) ;; *) exit 1 ;; esac
  printf '%s\n' "$value"
 done
done`

type diskPolicyEvidence struct {
	FileVault bool `json:"filevault"`
	Encrypted bool `json:"encrypted"`
	Locked    bool `json:"locked"`
}

type diskEvidence struct {
	System diskPolicyEvidence `json:"system"`
	Data   diskPolicyEvidence `json:"data"`
}

func parseDiskEvidence(output string) (diskEvidence, error) {
	var result diskEvidence
	lines := strings.Fields(output)
	if len(lines) != 6 {
		return result, domain.Err("disk_policy_unknown", "Cannot verify APFS encryption state; image will not be published")
	}
	values := []*bool{&result.System.FileVault, &result.System.Encrypted, &result.System.Locked, &result.Data.FileVault, &result.Data.Encrypted, &result.Data.Locked}
	for i, line := range lines {
		if line != "true" && line != "false" {
			return diskEvidence{}, domain.Err("disk_policy_unknown", "Cannot verify APFS encryption state; image will not be published")
		}
		*values[i] = line == "true"
	}
	return result, nil
}

func (d diskEvidence) validate() error {
	if d.System.FileVault || d.Data.FileVault {
		return domain.Err("disk_encrypted", "FileVault must be disabled during Setup Assistant; image will not be published")
	}
	if d.System.Encrypted || d.Data.Encrypted {
		return domain.Err("disk_encrypted", "APFS reports encryption at rest despite FileVault being off; an unencrypted golden is required and the image will not be published")
	}
	if d.System.Locked || d.Data.Locked {
		return domain.Err("disk_locked", "APFS boot volumes must be unlocked; image will not be published")
	}
	return nil
}

func (e *Engine) verifyDiskPolicy(ctx context.Context, l domain.Lease, g *guest) (diskEvidence, error) {
	out, err := g.Run(ctx, "/bin/bash -c "+shellQuote(diskPolicyProbe), "")
	if err != nil {
		return diskEvidence{}, domain.Err("disk_policy_unknown", "Cannot read APFS encryption state; image will not be published")
	}
	evidence, err := parseDiskEvidence(out)
	if err != nil {
		return evidence, err
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return evidence, err
	}
	if err := e.provisionLog(l, "disk-policy", string(encoded)); err != nil {
		return evidence, err
	}
	return evidence, evidence.validate()
}
