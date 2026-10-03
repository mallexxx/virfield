package lume

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mallexxx/virfield/internal/domain"
)

// Lume normally gives every clone a new virtual Mac identity. Monterey APFS
// encryption at rest binds the boot disk to the original machine identifier,
// so an explicitly identity-preserving legacy clone must keep that identity.
// The controller permits only one active worker with a legacy UUID.
func (c *Client) preserveLegacyIdentity(t domain.Template, l domain.Lease) error {
	if !domain.ValidUUID(t.LegacyUUID) || t.LegacyUUID != l.LegacyUUID || !domain.ValidName(t.Name) || !domain.ValidName(l.VMName) {
		return domain.Err("legacy_identity_failed", "legacy clone identity is invalid")
	}
	sourceRoot, targetRoot := c.StoragePaths[t.Location], c.StoragePaths[l.Location]
	if !filepath.IsAbs(sourceRoot) || !filepath.IsAbs(targetRoot) {
		return domain.Err("legacy_identity_failed", "legacy clone storage is not configured")
	}
	sourceDir := filepath.Join(sourceRoot, t.Name)
	targetDir := filepath.Join(targetRoot, l.VMName)
	for _, dir := range []string{sourceDir, targetDir} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return domain.Err("legacy_identity_failed", "legacy clone directory is missing or unsafe")
		}
	}
	for _, name := range []string{"nvram.bin"} {
		source, err := os.Lstat(filepath.Join(sourceDir, name))
		if err != nil || !source.Mode().IsRegular() || source.Size() == 0 {
			return domain.Err("legacy_identity_failed", "source legacy NVRAM is missing or unsafe")
		}
		clone, err := os.Lstat(filepath.Join(targetDir, name))
		if err != nil || !clone.Mode().IsRegular() || clone.Size() != source.Size() {
			return domain.Err("legacy_identity_failed", "cloned legacy NVRAM is missing or incomplete")
		}
	}
	source, _, err := readVMConfig(filepath.Join(sourceDir, "config.json"))
	if err != nil {
		return err
	}
	clone, mode, err := readVMConfig(filepath.Join(targetDir, "config.json"))
	if err != nil {
		return err
	}
	for _, field := range []string{"hardwareModel", "diskSize", "os"} {
		if len(source[field]) == 0 || !bytes.Equal(source[field], clone[field]) {
			return domain.Err("legacy_identity_failed", "cloned VM configuration differs from the source")
		}
	}
	for _, field := range []string{"machineIdentifier", "macAddress"} {
		var value string
		if err := json.Unmarshal(source[field], &value); err != nil || value == "" {
			return domain.Err("legacy_identity_failed", "source VM identity is missing")
		}
		clone[field] = source[field]
	}
	payload, err := json.Marshal(clone)
	if err != nil {
		return domain.Err("legacy_identity_failed", "legacy VM configuration could not be encoded")
	}
	return writeVMConfig(filepath.Join(targetDir, "config.json"), payload, mode)
}

func readVMConfig(path string) (map[string]json.RawMessage, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 64<<10 {
		return nil, 0, domain.Err("legacy_identity_failed", "legacy VM configuration is missing or unsafe")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, domain.Err("legacy_identity_failed", "legacy VM configuration could not be read")
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(b, &config); err != nil || config == nil {
		return nil, 0, domain.Err("legacy_identity_failed", "legacy VM configuration is invalid")
	}
	return config, info.Mode().Perm(), nil
}

func writeVMConfig(path string, payload []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".legacy-config-")
	if err != nil {
		return domain.Err("legacy_identity_failed", "legacy VM configuration could not be staged")
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return domain.Err("legacy_identity_failed", "legacy VM configuration could not be written")
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return domain.Err("legacy_identity_failed", "legacy VM configuration permissions failed")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return domain.Err("legacy_identity_failed", "legacy VM configuration sync failed")
	}
	if err := tmp.Close(); err != nil {
		return domain.Err("legacy_identity_failed", "legacy VM configuration close failed")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return domain.Err("legacy_identity_failed", "legacy VM configuration replace failed")
	}
	d, err := os.Open(dir)
	if err != nil {
		return domain.Err("legacy_identity_failed", "legacy VM directory could not be synced")
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return domain.Err("legacy_identity_failed", "legacy VM directory sync failed")
	}
	return nil
}
