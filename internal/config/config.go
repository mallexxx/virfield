// Package config loads explicit, validated host configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

type Config struct {
	ResourceLimits *domain.ResourceLimits `json:"resource_limits,omitempty"`
	StoragePaths   map[string]string      `json:"storage_paths,omitempty"`
	ImageTools     *domain.ImageTools     `json:"image_tools,omitempty"`
	Listen         string                 `json:"listen"`
	LumeURL        string                 `json:"lume_url"`
	StateDir       string                 `json:"state_dir"`
	TokenFile      string                 `json:"token_file"`
	MaxVMs         int                    `json:"max_vms"`
	Templates      []domain.Template      `json:"templates"`
}

func Load(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, errors.New("config must contain one JSON object")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return c, fmt.Errorf("listen: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return c, errors.New("listen must use a loopback IP; use an authenticated TLS proxy for remote access")
	}
	if !filepath.IsAbs(c.StateDir) || !filepath.IsAbs(c.TokenFile) {
		return c, errors.New("state_dir and token_file must be absolute paths")
	}
	if c.MaxVMs < 1 || c.MaxVMs > 2 {
		return c, errors.New("max_vms must be 1 or 2")
	}
	if c.ResourceLimits != nil && (c.ResourceLimits.CPU < 1 || c.ResourceLimits.MemoryBytes < 1 || c.ResourceLimits.DiskReserveBytes < 1) {
		return c, errors.New("resource limits must be positive")
	}
	for name, path := range c.StoragePaths {
		if !domain.ValidName(name) || !filepath.IsAbs(path) {
			return c, errors.New("storage paths must map valid names to absolute directories")
		}
	}
	return c, nil
}
func Token(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return "", errors.New("token file must be a regular file readable only by its owner (chmod 600)")
	}
	if st.Size() > 4096 {
		return "", errors.New("token file too large")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if len(s) < 32 || len(s) > 256 || strings.ContainsAny(s, " \r\n\t") {
		return "", errors.New("token must contain 32–256 characters without whitespace")
	}
	return s, nil
}
