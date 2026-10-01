package lume

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

func RegistryPull(ctx context.Context, binary string, l domain.Lease, s domain.RegistrySource, r domain.RegistryReference, token, logPath, storagePath string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := s.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(binary) || !domain.ValidName(l.VMName) || !domain.ValidName(l.Location) || s.ID != r.Source || s.Organization != r.Organization {
		return domain.Err("invalid_registry", "Invalid registry pull identity")
	}
	command := exec.CommandContext(ctx, binary, "pull", r.Repository+":"+r.Tag, l.VMName, "--registry", "ghcr.io", "--organization", r.Organization, "--storage", l.Location)
	environment, cleanup, err := registryEnvironment(l, s, token, logPath, storagePath)
	if err != nil {
		return err
	}
	defer cleanup()
	command.Env = environment
	return RunPrivate(ctx, command, logPath)
}

func RegistryPush(ctx context.Context, binary string, l domain.Lease, s domain.RegistrySource, repository, tag, token, logPath, storagePath string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(binary) || !l.Portable || !domain.ValidName(l.VMName) || !domain.ValidName(l.Location) || !s.AllowPush || token == "" || !domain.ValidRegistryRepository(repository) || !domain.ValidRegistryTag(tag) {
		return domain.Err("invalid_registry", "Invalid portable image publication")
	}
	command := exec.CommandContext(ctx, binary, "push", l.VMName, repository+":"+tag, "--registry", "ghcr.io", "--organization", s.Organization, "--storage", l.Location)
	environment, cleanup, err := registryEnvironment(l, s, token, logPath, storagePath)
	if err != nil {
		return err
	}
	defer cleanup()
	command.Env = environment
	return RunPrivate(ctx, command, logPath)
}

// Pinned Lume treats its default --registry/--organization values as absent and
// otherwise falls back to personal settings. Isolate XDG settings so neither
// registry credentials nor VM paths can be redirected by those defaults.
func registryEnvironment(l domain.Lease, s domain.RegistrySource, token, logPath, storagePath string) ([]string, func(), error) {
	invalid := func() ([]string, func(), error) {
		return nil, nil, domain.Err("invalid_registry", "Registry operations require explicit absolute storage and private log paths without YAML control characters")
	}
	if !filepath.IsAbs(storagePath) || !filepath.IsAbs(logPath) || strings.ContainsAny(storagePath+logPath, "\"\\\r\n\x00") {
		return invalid()
	}
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	settings, err := os.MkdirTemp(dir, "registry-settings-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(settings) }
	if err := os.Mkdir(filepath.Join(settings, "lume"), 0700); err != nil {
		cleanup()
		return nil, nil, err
	}
	config := "defaultLocationName: \"" + l.Location + "\"\ncacheDirectory: \"" + filepath.Join(dir, "registry-cache") + "\"\ncachingEnabled: false\ntelemetryEnabled: false\nvmLocations:\n  - name: \"" + l.Location + "\"\n    path: \"" + storagePath + "\"\nregistry:\n  type: \"ghcr\"\n  ghcr:\n    registry: \"ghcr.io\"\n    organization: \"" + s.Organization + "\"\n"
	if err := os.WriteFile(filepath.Join(settings, "lume", "config.yaml"), []byte(config), 0600); err != nil {
		cleanup()
		return nil, nil, err
	}
	env := []string{"LUME_TELEMETRY_ENABLED=false", "XDG_CONFIG_HOME=" + settings}
	for _, name := range []string{"HOME", "USER", "LOGNAME", "PATH", "TMPDIR"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	if token != "" {
		env = append(env, "GITHUB_USERNAME="+s.Username, "GITHUB_TOKEN="+token)
	}
	return env, cleanup, nil
}
