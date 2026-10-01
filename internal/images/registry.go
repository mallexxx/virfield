package images

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/lume"
	"github.com/mallexxx/virfield/internal/registry"
)

// An imported VM already has a native account. Offline setup replaces its
// directory record and can invalidate Secure Token / volume-owner credentials.
// Require the portable bootstrap contract and preserve that account in place.
func (e *Engine) setupRegistry(ctx context.Context, l domain.Lease, progress func(string) error) error {
	if err := progress("Preparing imported guest through SSH; preserving native account and volume ownership"); err != nil {
		return err
	}
	vm, err := e.boot(ctx, l)
	if err != nil {
		return err
	}
	g, err := e.connect(ctx, l, vm.IP, true)
	if err != nil {
		return domain.Err("registry_bootstrap_failed", "Imported guest must provide SSH for the lume account with bootstrap password lume; native account was not overwritten")
	}
	defer g.Close()
	if _, err := e.verifyDiskPolicy(ctx, l, g); err != nil {
		return err
	}
	if err := guestssh.BootstrapAutoLogin(ctx, g); err != nil {
		return err
	}
	if err := e.prepareDesktop(ctx, l, g); err != nil {
		return err
	}
	return e.stop(ctx, l)
}

func (e *Engine) registrySource(ref domain.RegistryReference) (domain.RegistrySource, error) {
	if e.Registry == nil {
		return domain.RegistrySource{}, domain.Err("registry_unconfigured", "Registry client is not configured")
	}
	source, err := e.Registry.Source(ref.Source)
	if err != nil {
		return source, err
	}
	if source.Organization != ref.Organization {
		return source, domain.Err("registry_source_changed", "Configured registry organization changed after request acceptance")
	}
	return source, nil
}
func (e *Engine) pullRegistry(ctx context.Context, l domain.Lease, ref domain.RegistryReference) error {
	source, err := e.registrySource(ref)
	if err != nil {
		return err
	}
	token, err := registry.Credentials(source)
	if err != nil {
		return err
	}
	root, ok := e.StoragePaths[l.Location]
	if !ok || !filepath.IsAbs(root) {
		return domain.Err("invalid_registry", "Registry pull needs an explicit configured storage path")
	}
	destination := filepath.Join(root, l.VMName)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return domain.Err("image_exists", "Registry pull never overwrites an existing VM directory")
	}
	if err := Space(e.Dir, ref.Size+(80<<30)); err != nil {
		return err
	}
	if err := Space(root, ref.Size+(80<<30)); err != nil {
		return err
	}
	if err := lume.RegistryPull(ctx, e.Tools.Lume, l, source, ref, token, filepath.Join(e.Dir, "images", l.ID, "registry-pull.log"), root); err != nil {
		return err
	}
	// Pinned Lume accepts tags, not digest references. Detect a moved tag before
	// any boot/setup command; a mismatching download is quarantined, never adopted.
	marker := filepath.Join(destination, ".manifest-digest")
	st, err := os.Lstat(marker)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 128 {
		return domain.Err("registry_digest_mismatch", "Pulled image has no valid manifest completion marker")
	}
	b, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(b)) != ref.Digest {
		return domain.Err("registry_digest_mismatch", "Registry tag changed during pull; downloaded VM will not be booted or published")
	}
	for _, name := range []string{"config.json", "nvram.bin", "disk.img"} {
		st, err := os.Lstat(filepath.Join(destination, name))
		if err != nil || !st.Mode().IsRegular() {
			return domain.Err("registry_invalid", "Pulled VM is incomplete or contains a non-regular VM file")
		}
	}
	return normalizeImportedVM(destination)
}

// Preserve virtual hardware identity needed to boot the imported disk, while
// discarding all optional configuration and applying the normal build budget.
// Never pass an unvalidated display string to Lume's Swift decoder (which
// force-unwraps it), or allow an imported bridged network configuration.
func normalizeImportedVM(dir string) error {
	invalid := func() error {
		return domain.Err("registry_invalid", "Pulled VM has invalid macOS configuration or exceeds the 512 GiB disk limit")
	}
	path := filepath.Join(dir, "config.json")
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return invalid()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return invalid()
	}
	var cfg struct {
		OS       string `json:"os"`
		CPU      int    `json:"cpuCount"`
		Memory   int64  `json:"memorySize"`
		Disk     int64  `json:"diskSize"`
		MAC      string `json:"macAddress"`
		Display  string `json:"display"`
		Hardware []byte `json:"hardwareModel"`
		Machine  []byte `json:"machineIdentifier"`
		Network  string `json:"networkMode"`
	}
	if json.Unmarshal(b, &cfg) != nil || !strings.EqualFold(cfg.OS, "macos") || len(cfg.Hardware) == 0 || len(cfg.Machine) == 0 {
		return invalid()
	}
	mac, err := net.ParseMAC(cfg.MAC)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
		return invalid()
	}
	disk, err := os.Lstat(filepath.Join(dir, "disk.img"))
	if err != nil || !disk.Mode().IsRegular() || disk.Size() < 16<<30 || disk.Size() > 512<<30 || cfg.Disk != disk.Size() {
		return invalid()
	}
	nvram, err := os.Lstat(filepath.Join(dir, "nvram.bin"))
	if err != nil || !nvram.Mode().IsRegular() || nvram.Size() == 0 || nvram.Size() > 64<<20 {
		return invalid()
	}
	cfg.OS, cfg.CPU, cfg.Memory, cfg.Display, cfg.Network = "macOS", 4, 8<<30, "1920x1440", "nat"
	b, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return invalid()
	}
	return os.WriteFile(path, b, 0600)
}
