package lume

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mallexxx/virfield/internal/domain"
)

func RegistryPull(ctx context.Context, binary string, l domain.Lease, s domain.RegistrySource, r domain.RegistryReference, token, logPath string) error {
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
	command.Env = []string{"LUME_TELEMETRY_ENABLED=false"}
	// Never pass a password through argv or inherit unrelated host credentials.
	for _, name := range []string{"HOME", "USER", "LOGNAME", "PATH", "TMPDIR"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	if token != "" {
		command.Env = append(command.Env, "GITHUB_USERNAME="+s.Username, "GITHUB_TOKEN="+token)
	}
	return RunPrivate(ctx, command, logPath)
}

func RegistryPush(ctx context.Context, binary string, l domain.Lease, s domain.RegistrySource, repository, tag, token, logPath string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(binary) || !l.Portable || !domain.ValidName(l.VMName) || !domain.ValidName(l.Location) || !s.AllowPush || token == "" || !domain.ValidRegistryRepository(repository) || !domain.ValidRegistryTag(tag) {
		return domain.Err("invalid_registry", "Invalid portable image publication")
	}
	command := exec.CommandContext(ctx, binary, "push", l.VMName, repository+":"+tag, "--registry", "ghcr.io", "--organization", s.Organization, "--storage", l.Location)
	command.Env = []string{"LUME_TELEMETRY_ENABLED=false"}
	for _, name := range []string{"HOME", "USER", "LOGNAME", "PATH", "TMPDIR"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	command.Env = append(command.Env, "GITHUB_USERNAME="+s.Username, "GITHUB_TOKEN="+token)
	return RunPrivate(ctx, command, logPath)
}
