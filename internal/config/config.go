// Package config loads explicit, validated host configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

type Config struct {
	Runtime                *Runtime                `json:"runtime,omitempty"`
	AllowedHosts           []string                `json:"allowed_hosts,omitempty"`
	FallbackCloneLocations []string                `json:"fallback_clone_locations,omitempty"`
	Principals             []Principal             `json:"principals,omitempty"`
	Registries             []domain.RegistrySource `json:"registries,omitempty"`
	ResourceLimits         *domain.ResourceLimits  `json:"resource_limits,omitempty"`
	StoragePaths           map[string]string       `json:"storage_paths,omitempty"`
	LegacyImageTools       json.RawMessage         `json:"image_tools,omitempty"`
	DefaultCloneLocation   string                  `json:"default_clone_location,omitempty"`
	Listen                 string                  `json:"listen"`
	LumeURL                string                  `json:"lume_url,omitempty"`
	StateDir               string                  `json:"state_dir"`
	TokenFile              string                  `json:"token_file"`
	MaxVMs                 int                     `json:"max_vms"`
	Templates              []domain.Template       `json:"templates"`
}

// Runtime selects one infrastructure authority for this virfieldd instance.
// Orchard may fan out to many remote workers; Tart manages this Mac directly.
// The infrastructure authority must be explicit; implicit legacy runtimes are rejected.
type Runtime struct {
	Provider string   `json:"provider"`
	Tart     *Tart    `json:"tart,omitempty"`
	Orchard  *Orchard `json:"orchard,omitempty"`
	// Nodes are physical hosts leased over SSH beside the primary runtime.
	Nodes []Node `json:"nodes,omitempty"`
}

// Node is a physical host controlled over pinned SSH as one unprivileged
// account. HostKey is the node's SSH host key in authorized-keys format;
// HostKeys adds the identities of other systems booted at the same address,
// such as the other macOS volumes of a multi-boot Mac.
type Node struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Address  string   `json:"address"`
	User     string   `json:"user"`
	HostKey  string   `json:"host_key"`
	HostKeys []string `json:"host_keys,omitempty"`
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	// KeepAwakeFile is honored by the node's own sleep guard while it exists.
	KeepAwakeFile string    `json:"keep_awake_file,omitempty"`
	WakeOnLAN     *NodeWake `json:"wake_on_lan,omitempty"`
	VMs           *NodeVMs  `json:"vms,omitempty"`
}

// NodeVMs runs disposable macOS VMs as rootless Podman containers on a Linux
// node. Goldens are read-only qcow2 files in GoldensDir; each lease publishes
// guest SSH on a port from PortBase, and image builds use the next Max ports.
type NodeVMs struct {
	Image      string `json:"image"`
	GoldensDir string `json:"goldens_dir"`
	PortBase   int    `json:"port_base"`
	Max        int    `json:"max"`
	CPU        int    `json:"cpu"`
	MemoryGB   int    `json:"memory_gb"`
	DiskSize   string `json:"disk_size"`
}

var (
	pinnedImage = regexp.MustCompile(`^[a-z0-9.-]+(/[a-z0-9._-]+)+@sha256:[0-9a-f]{64}$`)
	diskSize    = regexp.MustCompile(`^[1-9][0-9]*G$`)
)

func validateNodeVMs(n Node) error {
	v := n.VMs
	fail := func(what string) error { return errors.New("runtime node " + n.ID + " vms: " + what) }
	switch {
	case n.OS != "linux":
		return fail("supported on linux nodes")
	case !pinnedImage.MatchString(v.Image):
		return fail("image must be pinned by sha256 digest")
	case !filepath.IsAbs(v.GoldensDir) || strings.ContainsAny(v.GoldensDir, "'\"\n\r\t "):
		return fail("goldens_dir must be an absolute path without quotes or spaces")
	case v.Max < 1 || v.Max > 8:
		return fail("max must be 1-8")
	case v.PortBase < 1024 || v.PortBase+1000+2*v.Max > 65535:
		return fail("port_base must leave 2*max ports and their desktop ports (+1000) between 1024 and 65535")
	case v.CPU < 1 || v.MemoryGB < 4:
		return fail("cpu must be at least 1 and memory_gb at least 4")
	case !diskSize.MatchString(v.DiskSize):
		return fail("disk_size must look like 100G")
	}
	return nil
}

type NodeWake struct {
	MAC       string `json:"mac"`
	Broadcast string `json:"broadcast"`
}

type Tart struct {
	Binary              string                   `json:"binary"`
	HostID              string                   `json:"host_id"`
	HostName            string                   `json:"host_name,omitempty"`
	MaxVMs              int                      `json:"max_vms,omitempty"`
	CPULimit            int                      `json:"cpu_limit,omitempty"`
	MemoryReserveBytes  int64                    `json:"memory_reserve_bytes,omitempty"`
	DiskReserveBytes    int64                    `json:"disk_reserve_bytes,omitempty"`
	Containers          []TartContainer          `json:"containers"`
	RegistryCredentials []TartRegistryCredential `json:"registry_credentials,omitempty"`
	Image               *TartImage               `json:"image,omitempty"`
	Bases               []domain.TartBase        `json:"bases,omitempty"`
}

type TartRegistryCredential struct {
	Host      string `json:"host"`
	Username  string `json:"username"`
	TokenFile string `json:"token_file"`
}

// TartImage enables Tart image adoption. Paths are operator-owned host paths.
type TartImage struct {
	Packer        string `json:"packer"`
	XcodeArchives string `json:"xcode_archives"`
}

type TartContainer struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	VolumeUUID string `json:"volume_uuid,omitempty"`
}

type Orchard struct {
	URL                       string               `json:"url"`
	ServiceAccount            string               `json:"service_account"`
	TokenFile                 string               `json:"token_file"`
	PoolID                    string               `json:"pool_id"`
	WorkerLabels              map[string]string    `json:"worker_labels,omitempty"`
	HostLimits                map[string]HostLimit `json:"host_limits,omitempty"`
	MaxVMs                    int                  `json:"max_vms,omitempty"`
	WorkerOfflineAfterSeconds int                  `json:"worker_offline_after_seconds,omitempty"`
}

type HostLimit struct {
	MaxVMs             int        `json:"max_vms"`
	MemoryReserveBytes int64      `json:"memory_reserve_bytes"`
	WakeOnLAN          *WakeOnLAN `json:"wake_on_lan,omitempty"`
}

type WakeOnLAN struct {
	MACAddress       string `json:"mac_address"`
	BroadcastAddress string `json:"broadcast_address"`
}

type Principal struct {
	Name      string   `json:"name"`
	TokenFile string   `json:"token_file"`
	Scopes    []string `json:"scopes"`
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
	if c.MaxVMs < 1 || c.MaxVMs > domain.MaxVMs {
		return c, fmt.Errorf("max_vms must be between 1 and %d", domain.MaxVMs)
	}
	if err := validateRuntime(&c); err != nil {
		return c, err
	}
	for _, allowed := range c.AllowedHosts {
		host, port, err := net.SplitHostPort(allowed)
		n, nerr := strconv.Atoi(port)
		if err != nil || host == "" || nerr != nil || n < 1 || n > 65535 || strings.ContainsAny(host, "/@\\ ") {
			return c, errors.New("allowed_hosts must contain exact host:port authorities")
		}
	}
	seenPrincipals := map[string]bool{}
	for _, p := range c.Principals {
		if !domain.ValidName(p.Name) || p.Name == "operator" || seenPrincipals[p.Name] || !filepath.IsAbs(p.TokenFile) || len(p.Scopes) == 0 {
			return c, errors.New("principals need unique names, absolute token files and scopes")
		}
		seenPrincipals[p.Name] = true
		for _, scope := range p.Scopes {
			if scope != "lease:own" && scope != "image:build" {
				return c, errors.New("principal scope must be lease:own or image:build")
			}
		}
	}
	if c.ResourceLimits != nil && (c.ResourceLimits.CPU < 1 || c.ResourceLimits.MemoryBytes < 1 || c.ResourceLimits.DiskReserveBytes < 1) {
		return c, errors.New("resource limits must be positive")
	}
	for name, path := range c.StoragePaths {
		if !domain.ValidName(name) || !filepath.IsAbs(path) {
			return c, errors.New("storage paths must map valid names to absolute directories")
		}
	}
	storagePaths := c.StoragePaths
	if storagePaths == nil {
		storagePaths = map[string]string{"home": ""}
	}
	cloneLocations := append([]string{c.DefaultCloneLocation}, c.FallbackCloneLocations...)
	seenCloneLocations := map[string]bool{}
	for _, name := range cloneLocations {
		if name == "" {
			continue
		}
		if !domain.ValidName(name) || storagePaths[name] == "" && c.StoragePaths != nil {
			return c, errors.New("clone locations must reference configured storage paths")
		}
		if seenCloneLocations[name] {
			return c, errors.New("clone locations must be unique")
		}
		seenCloneLocations[name] = true
	}
	seenRegistries := map[string]bool{}
	for _, source := range c.Registries {
		if err := source.Validate(); err != nil {
			return c, err
		}
		if seenRegistries[source.ID] {
			return c, errors.New("duplicate registry source")
		}
		seenRegistries[source.ID] = true
	}
	return c, nil
}

func validateRuntime(c *Config) error {
	if c.Runtime == nil {
		return errors.New("runtime is required; migrate retired Lume deployments to Tart")
	}
	if len(c.LegacyImageTools) != 0 && string(c.LegacyImageTools) != "null" {
		return errors.New("image_tools is retired; configure runtime.tart.image")
	}
	if err := validateNodes(c.Runtime); err != nil {
		return err
	}
	switch c.Runtime.Provider {
	case "tart":
		if c.Runtime.Tart == nil || c.Runtime.Orchard != nil {
			return errors.New("runtime tart requires only the tart configuration")
		}
		t := c.Runtime.Tart
		for _, base := range t.Bases {
			if err := base.Validate(); err != nil {
				return err
			}
		}
		if c.ResourceLimits != nil {
			return errors.New("runtime-specific host limits replace legacy resource_limits")
		}
		if !filepath.IsAbs(t.Binary) || !domain.ValidName(t.HostID) || len(t.Containers) == 0 {
			return errors.New("tart runtime requires an absolute binary, host_id and containers")
		}
		if t.HostName == "" {
			t.HostName = t.HostID
		}
		if t.MaxVMs == 0 {
			t.MaxVMs = c.MaxVMs
		}
		if t.CPULimit < 0 {
			return errors.New("tart cpu_limit must be positive when configured")
		}
		if t.MemoryReserveBytes == 0 {
			t.MemoryReserveBytes = 4 << 30
		}
		if t.MemoryReserveBytes < 1 {
			return errors.New("tart memory_reserve_bytes must be positive")
		}
		if t.DiskReserveBytes == 0 {
			t.DiskReserveBytes = 20 << 30
		}
		if t.DiskReserveBytes < 1 {
			return errors.New("tart disk_reserve_bytes must be positive")
		}
		if t.MaxVMs < 1 || t.MaxVMs > domain.MaxVMs {
			return errors.New("tart max_vms is out of range")
		}
		seen := map[string]bool{}
		paths := make(map[string]string, len(t.Containers))
		for _, container := range t.Containers {
			if !domain.ValidName(container.ID) || seen[container.ID] || !filepath.IsAbs(container.Path) {
				return errors.New("tart containers require unique IDs and absolute paths")
			}
			if container.Kind != "local" && container.Kind != "external" {
				return errors.New("tart container kind must be local or external")
			}
			if container.VolumeUUID != "" && !domain.ValidUUID(container.VolumeUUID) {
				return errors.New("tart container volume_uuid must be a UUID")
			}
			seen[container.ID] = true
			paths[container.ID] = container.Path
		}
		credentialHosts := map[string]bool{}
		for _, credential := range t.RegistryCredentials {
			if credential.Host == "" || strings.ContainsAny(credential.Host, "/:@ \\") || credentialHosts[credential.Host] || credential.Username == "" || !filepath.IsAbs(credential.TokenFile) {
				return errors.New("tart registry credentials require a unique host, username and absolute token_file")
			}
			credentialHosts[credential.Host] = true
		}
		if t.Image != nil && (!filepath.IsAbs(t.Image.Packer) || !filepath.IsAbs(t.Image.XcodeArchives)) {
			return errors.New("tart image requires absolute packer and xcode_archives paths")
		}
		if c.StoragePaths != nil {
			return errors.New("tart containers replace legacy storage_paths")
		}
		c.StoragePaths = paths
	case "orchard":
		if c.Runtime.Orchard == nil || c.Runtime.Tart != nil {
			return errors.New("runtime orchard requires only the orchard configuration")
		}
		o := c.Runtime.Orchard
		if c.ResourceLimits != nil {
			return errors.New("runtime-specific host limits replace legacy resource_limits")
		}
		u, err := url.Parse(o.URL)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("orchard url must be an http(s) origin")
		}
		if u.Scheme != "https" {
			ip := net.ParseIP(u.Hostname())
			if u.Scheme != "http" || u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
				return errors.New("remote orchard url must use HTTPS")
			}
		}
		if !domain.ValidName(o.ServiceAccount) || !filepath.IsAbs(o.TokenFile) || !domain.ValidName(o.PoolID) {
			return errors.New("orchard requires service_account, absolute token_file and pool_id")
		}
		for key, value := range o.WorkerLabels {
			if !domain.ValidName(key) || !domain.ValidName(value) {
				return errors.New("orchard worker_labels must use valid names")
			}
		}
		for hostID, limit := range o.HostLimits {
			if !domain.ValidName(hostID) || limit.MaxVMs < 1 || limit.MaxVMs > domain.MaxVMs || limit.MemoryReserveBytes < 1 {
				return errors.New("orchard host_limits require a valid host, max_vms and memory_reserve_bytes")
			}
			if limit.WakeOnLAN != nil {
				mac, macErr := net.ParseMAC(limit.WakeOnLAN.MACAddress)
				host, port, addressErr := net.SplitHostPort(limit.WakeOnLAN.BroadcastAddress)
				portNumber, portErr := strconv.Atoi(port)
				ip := net.ParseIP(host)
				if macErr != nil || len(mac) != 6 || addressErr != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || ip == nil || ip.To4() == nil {
					return errors.New("wake_on_lan requires a six-byte MAC and IPv4 broadcast host:port")
				}
			}
		}
		if o.MaxVMs == 0 {
			o.MaxVMs = c.MaxVMs
		}
		if o.MaxVMs < 1 || o.MaxVMs > domain.MaxVMs {
			return errors.New("orchard max_vms is out of range")
		}
		if o.WorkerOfflineAfterSeconds == 0 {
			o.WorkerOfflineAfterSeconds = 30
		}
		if o.WorkerOfflineAfterSeconds < 5 || o.WorkerOfflineAfterSeconds > 3600 {
			return errors.New("orchard worker_offline_after_seconds must be between 5 and 3600")
		}
	default:
		return errors.New("runtime provider must be tart or orchard")
	}
	return nil
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

func validateNodes(r *Runtime) error {
	if len(r.Nodes) == 0 {
		return nil
	}
	if r.Provider != "tart" || r.Tart == nil {
		return errors.New("runtime nodes are supported beside the tart runtime")
	}
	ids := map[string]bool{}
	for _, container := range r.Tart.Containers {
		ids[container.ID] = true
	}
	for _, n := range r.Nodes {
		if !domain.ValidName(n.ID) || ids[n.ID] {
			return errors.New("runtime nodes need unique IDs distinct from storage containers")
		}
		ids[n.ID] = true
		if n.Address == "" || strings.ContainsAny(n.Address, "@/ \\") || !domain.ValidName(n.User) {
			return errors.New("runtime node " + n.ID + " needs an address and a user")
		}
		for _, key := range append([]string{n.HostKey}, n.HostKeys...) {
			if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err != nil {
				return errors.New("runtime node " + n.ID + " host keys must be SSH public keys")
			}
		}
		if n.KeepAwakeFile != "" && (!filepath.IsAbs(n.KeepAwakeFile) || strings.ContainsAny(n.KeepAwakeFile, "'\"\n\r\t ")) {
			return errors.New("runtime node " + n.ID + " keep_awake_file must be an absolute path without quotes or spaces")
		}
		if (n.OS != "macOS" && n.OS != "linux") || (n.Arch != "amd64" && n.Arch != "arm64") {
			return errors.New("runtime node " + n.ID + " needs os macOS|linux and arch amd64|arm64")
		}
		if n.VMs != nil {
			if err := validateNodeVMs(n); err != nil {
				return err
			}
		}
		if w := n.WakeOnLAN; w != nil {
			mac, err := net.ParseMAC(w.MAC)
			if _, _, splitErr := net.SplitHostPort(w.Broadcast); err != nil || len(mac) != 6 || splitErr != nil {
				return errors.New("runtime node " + n.ID + " wake_on_lan needs a MAC and broadcast host:port")
			}
		}
	}
	return nil
}
