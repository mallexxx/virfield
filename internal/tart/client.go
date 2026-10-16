// Package tart adapts the Tart CLI to Virfield's narrow runtime contract.
// Each configured storage container gets its own TART_HOME.
package tart

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/hostresources"
)

const outputLimit = 4 << 20

type Config struct {
	Binary              string
	HostID              string
	HostName            string
	MaxVMs              int
	CPULimit            int
	MemoryReserveBytes  int64
	Containers          []ContainerConfig
	RegistryCredentials []RegistryCredential
}

type RegistryCredential struct {
	Host     string
	Username string
	Token    string
}

type ContainerConfig struct {
	ID         string
	Path       string
	Kind       string
	VolumeUUID string
}

type volume struct {
	Identity string
	Total    int64
	Free     int64
}

type commandRunner interface {
	Run(context.Context, string, []string) ([]byte, error)
	StartDetached(string, []string) error
}

type Client struct {
	config   Config
	runner   commandRunner
	probe    func(context.Context, string) (volume, error)
	now      func() time.Time
	capacity domain.HostCapacity
	specMu   sync.Mutex
	specs    map[string]vmSpec
	// A transfer holds Tart's config lock. Inventory must not open/close that
	// file: POSIX record locks are process-wide and any close releases them.
	transfers map[string]bool
}

// vmSpec is a VM's static configuration, valid while its config.json is
// unchanged. tart get on an ASIF VM attaches and mounts the disk image to
// size it (and Finder then crawls the volume), so it must not run on every
// inventory refresh.
type vmSpec struct {
	modified time.Time
	size     int64
	os       string
	cpu      int
	memory   int64
	disk     int64
}

func New(config Config) (*Client, error) {
	if !filepath.IsAbs(config.Binary) || !domain.ValidName(config.HostID) || config.MaxVMs < 1 || len(config.Containers) == 0 {
		return nil, errors.New("invalid Tart runtime configuration")
	}
	if config.HostName == "" {
		config.HostName = config.HostID
	}
	totalMemory, err := hostresources.Memory()
	if err != nil {
		return nil, err
	}
	if config.MemoryReserveBytes == 0 {
		config.MemoryReserveBytes = 4 << 30
	}
	if config.MemoryReserveBytes >= totalMemory {
		return nil, errors.New("tart memory reserve leaves no allocatable RAM")
	}
	if config.CPULimit < 1 {
		config.CPULimit = runtime.NumCPU()
	}
	if config.CPULimit > runtime.NumCPU() {
		return nil, errors.New("tart CPU limit exceeds host logical CPUs")
	}
	capacity := domain.HostCapacity{
		Total:              domain.Resources{CPU: runtime.NumCPU(), MemoryBytes: totalMemory},
		Allocatable:        domain.Resources{CPU: config.CPULimit, MemoryBytes: totalMemory - config.MemoryReserveBytes},
		MemoryReserveBytes: config.MemoryReserveBytes, MaxVMs: config.MaxVMs,
	}
	seen := map[string]bool{}
	for _, container := range config.Containers {
		if !domain.ValidName(container.ID) || seen[container.ID] || !filepath.IsAbs(container.Path) || container.Kind != "local" && container.Kind != "external" {
			return nil, errors.New("invalid Tart storage container")
		}
		seen[container.ID] = true
	}
	credentials := make(map[string]RegistryCredential, len(config.RegistryCredentials))
	for _, credential := range config.RegistryCredentials {
		if credential.Host == "" || credential.Username == "" || credential.Token == "" || credentials[credential.Host].Host != "" {
			return nil, errors.New("invalid Tart registry credential")
		}
		credentials[credential.Host] = credential
	}
	r := &execRunner{binary: config.Binary, credentials: credentials}
	volumes := &volumeProbe{stat: statVolume, lookup: r.probeVolume}
	return &Client{config: config, runner: r, probe: volumes.read, now: func() time.Time { return time.Now().UTC() }, capacity: capacity}, nil
}

func (c *Client) ProviderName() string { return "tart" }

func (c *Client) ConfiguredInventory() domain.Observation {
	o := domain.Observation{
		VMs: []domain.VM{}, Provider: c.ProviderName(), HostMax: c.config.MaxVMs,
		Hosts: []domain.Host{{
			ID: c.config.HostID, Name: c.config.HostName, Provider: c.ProviderName(), OS: "macOS", Arch: runtime.GOARCH,
			Availability: "unavailable", UnavailableReason: "not_observed", Capacity: c.capacity,
		}},
	}
	for _, configured := range c.config.Containers {
		o.StorageContainers = append(o.StorageContainers, domain.StorageContainer{
			ID: configured.ID, HostID: c.config.HostID, Provider: c.ProviderName(), Kind: configured.Kind, Path: configured.Path,
			Availability: "unavailable", UnavailableReason: "not_observed",
		})
	}
	return o
}

func (c *Client) Observe(ctx context.Context) (domain.Observation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	now := c.now()
	o := domain.Observation{
		VMs:      []domain.VM{},
		Provider: c.ProviderName(),
		HostMax:  c.config.MaxVMs,
		At:       now,
		Hosts: []domain.Host{{
			ID: c.config.HostID, Name: c.config.HostName, Provider: c.ProviderName(),
			OS: "macOS", Arch: runtime.GOARCH, Availability: "available", LastSeenAt: now, Capacity: c.capacity,
		}},
	}
	availableContainers := 0
	seenVMs := map[string]bool{}
	for _, configured := range c.config.Containers {
		container := domain.StorageContainer{ID: configured.ID, HostID: c.config.HostID, Provider: c.ProviderName(), Kind: configured.Kind, Path: configured.Path}
		info, err := c.probe(ctx, configured.Path)
		if err := ctx.Err(); err != nil {
			return domain.Observation{}, err
		}
		if err != nil {
			container.Availability = "unavailable"
			container.UnavailableReason = volumeReason(err)
			o.StorageContainers = append(o.StorageContainers, container)
			continue
		}
		container.DeviceIdentity = info.Identity
		container.CapacityBytes = info.Total
		container.AvailableBytes = info.Free
		container.LastSeenAt = now
		if configured.VolumeUUID != "" && !strings.EqualFold(configured.VolumeUUID, info.Identity) {
			container.Availability = "unavailable"
			container.UnavailableReason = "volume_identity_mismatch"
			o.StorageContainers = append(o.StorageContainers, container)
			continue
		}
		container.Availability = "available"
		availableContainers++
		o.StorageContainers = append(o.StorageContainers, container)

		rows, err := c.list(ctx, configured)
		if err != nil {
			return domain.Observation{}, fmt.Errorf("tart inventory for %s: %w", configured.ID, err)
		}
		vms, err := c.inspectAll(ctx, configured, rows, now)
		if err := ctx.Err(); err != nil {
			return domain.Observation{}, err
		}
		if err != nil {
			return domain.Observation{}, err
		}
		for _, vm := range vms {
			if seenVMs[vm.Key()] {
				return domain.Observation{}, errors.New("duplicate Tart VM identity")
			}
			seenVMs[vm.Key()] = true
			o.VMs = append(o.VMs, vm)
			if vm.State != "stopped" {
				o.HostUsed++
			}
		}
	}
	if availableContainers == 0 {
		// Keep inventory visible, but close admission while no configured storage
		// can safely prove its device identity.
		o.HostUsed = o.HostMax
	}
	allocated := domain.Resources{}
	for _, vm := range o.VMs {
		if vm.State == "stopped" || vm.State == "failed" {
			continue
		}
		allocated.CPU += vm.Resources.CPU
		allocated.MemoryBytes += vm.Resources.MemoryBytes
		allocated.DiskBytes += vm.Resources.DiskBytes
	}
	o.Hosts[0].Capacity.Allocated = allocated
	o.Hosts[0].Capacity.UsedVMs = o.HostUsed
	o.Hosts[0].Capacity.Available = subtractResources(o.Hosts[0].Capacity.Allocatable, allocated)
	return o, nil
}

func subtractResources(total, used domain.Resources) domain.Resources {
	return domain.Resources{CPU: max(0, total.CPU-used.CPU), MemoryBytes: max(0, total.MemoryBytes-used.MemoryBytes), DiskBytes: max(0, total.DiskBytes-used.DiskBytes)}
}

type listRow struct {
	Name    string `json:"Name"`
	Running bool   `json:"Running"`
	State   string `json:"State"`
}

// list enumerates the container's VMs without tart list, which sizes (and so
// attaches and mounts) every ASIF disk on each call. A VM is running while its
// tart run process holds the fcntl lock on config.json, as tart list checks.
func (c *Client) list(_ context.Context, container ContainerConfig) ([]listRow, error) {
	entries, err := os.ReadDir(filepath.Join(container.Path, "vms"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows := []listRow{}
	for _, entry := range entries {
		config := filepath.Join(container.Path, "vms", entry.Name(), "config.json")
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(config); err != nil {
			continue
		}
		c.specMu.Lock()
		running, err := false, error(nil)
		if !c.transfers[config] {
			running, err = runLocked(config)
		}
		c.specMu.Unlock()
		if err != nil {
			return nil, err
		}
		state := "stopped"
		if running {
			state = "running"
		}
		rows = append(rows, listRow{Name: entry.Name(), Running: running, State: state})
	}
	return rows, nil
}

// runLocked reports whether another process holds a lock on the file.
// Closing this descriptor cannot release that lock: fcntl locks belong to
// their owning process, and this process never locks VM files.
func runLocked(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lock); err != nil {
		return false, err
	}
	return lock.Type != syscall.F_UNLCK, nil
}

// inspectAll runs tart get for every VM with bounded concurrency and keeps
// list order. tart get computes allocated size, about a second per ASIF disk.
func (c *Client) inspectAll(ctx context.Context, container ContainerConfig, rows []listRow, now time.Time) ([]domain.VM, error) {
	vms := make([]domain.VM, len(rows))
	errs := make([]error, len(rows))
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, row := range rows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			vms[i], errs[i] = c.inspect(ctx, container, row, now)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("tart inspect %s/%s: %w", container.ID, rows[i].Name, err)
		}
	}
	return vms, nil
}

// spec returns the VM's static configuration, running tart get only when the
// VM's config.json changed since the last read (or cannot be inspected).
func (c *Client) spec(ctx context.Context, container ContainerConfig, name string) (vmSpec, error) {
	key := container.ID + "/" + name
	info, statErr := os.Stat(filepath.Join(container.Path, "vms", name, "config.json"))
	if statErr == nil {
		c.specMu.Lock()
		cached, ok := c.specs[key]
		c.specMu.Unlock()
		if ok && cached.disk > 0 && cached.modified.Equal(info.ModTime()) && cached.size == info.Size() {
			return cached, nil
		}
	}
	var raw struct {
		OS     string   `json:"OS"`
		CPU    int      `json:"CPU"`
		Memory int64    `json:"Memory"`
		Disk   *float64 `json:"Disk"`
	}
	b, err := c.runner.Run(ctx, container.Path, []string{"get", name, "--format", "json"})
	if err != nil {
		return vmSpec{}, err
	}
	if err := json.Unmarshal(b, &raw); err != nil || raw.CPU < 1 || raw.Memory < 1 || raw.OS == "" {
		return vmSpec{}, errors.New("invalid Tart get response")
	}
	spec := vmSpec{os: normalizeOS(raw.OS), cpu: raw.CPU, memory: raw.Memory << 20}
	if raw.Disk != nil {
		spec.disk = int64(*raw.Disk * 1_000_000_000)
	}
	// A running ASIF can temporarily report no virtual disk size. Stopping
	// the VM does not change config.json, so caching that incomplete response
	// would permanently block worker admission after a successful image build.
	// Unknown stays unknown for this observation; only complete specs are cached.
	if statErr == nil && spec.disk > 0 {
		spec.modified, spec.size = info.ModTime(), info.Size()
		c.specMu.Lock()
		if c.specs == nil {
			c.specs = map[string]vmSpec{}
		}
		c.specs[key] = spec
		c.specMu.Unlock()
	}
	return spec, nil
}

func (c *Client) inspect(ctx context.Context, container ContainerConfig, row listRow, now time.Time) (domain.VM, error) {
	if !domain.ValidName(row.Name) || row.State == "" {
		return domain.VM{}, errors.New("invalid Tart VM record")
	}
	spec, err := c.spec(ctx, container, row.Name)
	if err != nil {
		return domain.VM{}, err
	}
	state := row.State
	if row.Running {
		state = "running"
	}
	if state == "Stopped" || state == "stopped" {
		state = "stopped"
	}
	vm := domain.VM{
		RuntimeID: c.currentRuntimeIdentity(filepath.Join(container.Path, "vms", row.Name, "config.json")),
		Resources: domain.Resources{CPU: spec.cpu, MemoryBytes: spec.memory, DiskBytes: spec.disk},
		Name:      row.Name, Location: container.ID, Provider: c.ProviderName(), HostID: c.config.HostID,
		ContainerID: container.ID, OS: spec.os, State: state, LastSeenAt: now,
	}
	if state == "running" {
		ip, ipErr := c.runner.Run(ctx, container.Path, []string{"ip", row.Name, "--wait", "0"})
		// An interrupted lookup says nothing about the VM's current address.
		// Reject the whole refresh so reconciliation keeps the last inventory
		// instead of recording a false address change or missing VM.
		if err := ctx.Err(); err != nil {
			return domain.VM{}, err
		}
		if ipErr == nil {
			candidate := strings.TrimSpace(string(ip))
			if net.ParseIP(candidate) != nil {
				vm.IP = candidate
				vm.SSHAvailable = true
			}
		}
	}
	return vm, nil
}

func normalizeOS(value string) string {
	switch strings.ToLower(value) {
	case "darwin", "macos":
		return "macOS"
	case "linux":
		return "linux"
	default:
		return value
	}
}

func (c *Client) Clone(ctx context.Context, template domain.Template, lease domain.Lease) error {
	container, err := c.container(lease.Location)
	if err != nil {
		return err
	}
	source := template.Source
	if source == "" {
		if template.Location != lease.Location {
			return errors.New("tart cannot clone a local VM across TART_HOME containers")
		}
		source = template.Name
	}
	_, err = c.runner.Run(ctx, container.Path, []string{"clone", source, lease.VMName})
	if err != nil {
		return err
	}
	// Pixel units use a 72-PPI display independently of the host's Retina scale.
	// Point units can leave Monterey at 3840x2160 without a 1920x1080 mode.
	// Disallow window refitting so desktop clients cannot shrink the GUI.
	_, err = c.runner.Run(ctx, container.Path, []string{"set", lease.VMName, "--display", "1920x1080px", "--no-display-refit"})
	return err
}

func (c *Client) Start(ctx context.Context, lease domain.Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	container, err := c.container(lease.Location)
	if err != nil {
		return err
	}
	return c.runner.StartDetached(container.Path, []string{"run", "--no-graphics", lease.VMName})
}

func (c *Client) Stop(ctx context.Context, lease domain.Lease) error {
	container, err := c.container(lease.Location)
	if err != nil {
		return err
	}
	_, err = c.runner.Run(ctx, container.Path, []string{"stop", lease.VMName})
	return err
}

func (c *Client) Delete(ctx context.Context, lease domain.Lease) error {
	container, err := c.container(lease.Location)
	if err != nil {
		return err
	}
	_, err = c.runner.Run(ctx, container.Path, []string{"delete", lease.VMName})
	return err
}

func (c *Client) container(id string) (ContainerConfig, error) {
	for _, container := range c.config.Containers {
		if container.ID == id {
			return container, nil
		}
	}
	return ContainerConfig{}, fmt.Errorf("unknown Tart storage container %q", id)
}

type execRunner struct {
	binary      string
	credentials map[string]RegistryCredential
}

func (r *execRunner) environment(home string, args []string) []string {
	environment := append(os.Environ(), "TART_HOME="+home, "TART_NO_AUTO_PRUNE=1")
	for host, credential := range r.credentials {
		prefix := host + "/"
		for _, argument := range args {
			if strings.HasPrefix(argument, prefix) {
				return append(environment,
					"TART_REGISTRY_HOSTNAME="+host,
					"TART_REGISTRY_USERNAME="+credential.Username,
					"TART_REGISTRY_PASSWORD="+credential.Token,
				)
			}
		}
	}
	return environment
}

func (r *execRunner) Run(ctx context.Context, home string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Env = r.environment(home, args)
	// A descendant may inherit stdout/stderr after the CLI exits. Bound the
	// pipe drain so a timed-out inventory read cannot stall reconciliation.
	cmd.WaitDelay = 2 * time.Second
	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tart command failed: %w", err)
	}
	return output.Bytes(), nil
}

func (r *execRunner) StartDetached(home string, args []string) error {
	cmd := exec.Command(r.binary, args...)
	cmd.Env = r.environment(home, args)
	stderr := &cappedWriter{limit: 4096}
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tart command failed: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// tart run exits at once when it cannot start, e.g. while the VM is
	// still held by another tart process; report that instead of a start.
	select {
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("tart %s exited: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
	case <-time.After(startGrace):
	}
	return nil
}

// startGrace is how long a detached tart command must survive to count as started.
var startGrace = 3 * time.Second

// cappedWriter keeps the first bytes for diagnostics and discards the rest, so
// a long-lived child never blocks on a full pipe.
type cappedWriter struct {
	mu    sync.Mutex
	limit int
	b     bytes.Buffer
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := w.limit - w.b.Len(); room > 0 {
		w.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (w *cappedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

var errOutputLimit = errors.New("command output exceeds 4 MiB")

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > outputLimit {
		return 0, errOutputLimit
	}
	return b.Buffer.Write(p)
}

var errVolumeUnmounted = errors.New("volume unmounted")

func volumeReason(err error) string {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, errVolumeUnmounted) {
		return "volume_unmounted"
	}
	return "volume_probe_failed"
}

func (r *execRunner) probeVolume(ctx context.Context, path string) (volume, error) {
	st, err := os.Stat(path)
	if err != nil {
		return volume{}, err
	}
	if !st.IsDir() {
		return volume{}, errors.New("tart storage path is not a directory")
	}
	// diskutil accepts only a mount point or device, not a nested directory.
	mount, err := mountPoint(path)
	if err != nil {
		return volume{}, err
	}
	cmd := exec.CommandContext(ctx, "/usr/sbin/diskutil", "info", "-plist", mount)
	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return volume{}, fmt.Errorf("diskutil info: %w", err)
	}
	values, err := parsePlist(output.Bytes())
	if err != nil {
		return volume{}, err
	}
	identity := values["VolumeUUID"]
	if identity == "" {
		identity = values["DiskUUID"]
	}
	if identity == "" {
		return volume{}, errors.New("volume has no stable UUID")
	}
	total, err := diskSize(values, "TotalSize", "APFSContainerSize")
	if err != nil {
		return volume{}, errors.New("invalid volume total size")
	}
	free, err := diskSize(values, "FreeSpace", "APFSContainerFree")
	if err != nil {
		return volume{}, errors.New("invalid volume free space")
	}
	return volume{Identity: strings.ToLower(identity), Total: total, Free: free}, nil
}

func diskSize(values map[string]string, primary, apfsFallback string) (int64, error) {
	value, err := strconv.ParseInt(values[primary], 10, 64)
	if err == nil && value > 0 {
		return value, nil
	}
	fallback, fallbackErr := strconv.ParseInt(values[apfsFallback], 10, 64)
	if fallbackErr == nil && fallback >= 0 {
		return fallback, nil
	}
	if err != nil {
		return 0, err
	}
	return value, nil
}

func parsePlist(data []byte) (map[string]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	values := map[string]string{}
	var key string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("invalid diskutil plist")
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return nil, err
			}
		case "string", "integer":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return nil, err
			}
			if key != "" {
				values[key] = value
				key = ""
			}
		}
	}
	return values, nil
}
