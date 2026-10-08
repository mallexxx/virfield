// Package orchard adapts the Orchard controller HTTP API to Virfield's
// runtime contract without importing Orchard's full worker dependency graph.
package orchard

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/wol"
)

const responseLimit = 4 << 20

type Config struct {
	URL                       string
	ServiceAccount            string
	Token                     string
	PoolID                    string
	WorkerLabels              map[string]string
	HostLimits                map[string]HostLimit
	MaxVMs                    int
	WorkerOfflineAfterSeconds int
}

type HostLimit struct {
	MaxVMs             int
	MemoryReserveBytes int64
	WakeOnLAN          *WakeOnLAN
}

type WakeOnLAN struct {
	MACAddress       string
	BroadcastAddress string
}

type Client struct {
	base   *url.URL
	auth   string
	config Config
	http   *http.Client
	now    func() time.Time
}

type RejectedMutation struct{ Status int }

func (e *RejectedMutation) Error() string {
	return fmt.Sprintf("Orchard rejected mutation with HTTP %d", e.Status)
}
func (e *RejectedMutation) Definitive() bool { return true }

func New(config Config) (*Client, error) {
	u, err := url.Parse(config.URL)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("Orchard URL must be an HTTP(S) origin")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("remote Orchard URL must use HTTPS")
		}
	}
	if !domain.ValidName(config.ServiceAccount) || config.Token == "" || !domain.ValidName(config.PoolID) || config.MaxVMs < 1 {
		return nil, errors.New("invalid Orchard runtime configuration")
	}
	if config.WorkerOfflineAfterSeconds == 0 {
		config.WorkerOfflineAfterSeconds = 30
	}
	transport := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		DialContext:     (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConns:    16, MaxIdleConnsPerHost: 8, IdleConnTimeout: 30 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	return &Client{
		base: u, auth: config.ServiceAccount + ":" + config.Token, config: config,
		http: &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		now:  func() time.Time { return time.Now().UTC() },
	}, nil
}

func (c *Client) ProviderName() string { return "orchard" }
func (c *Client) CreateStartsVM() bool { return true }

func (c *Client) ConfiguredInventory() domain.Observation {
	o := domain.Observation{VMs: []domain.VM{}, Provider: c.ProviderName(), HostMax: c.config.MaxVMs}
	for _, hostID := range sortedHostIDs(c.config.HostLimits) {
		limit := c.config.HostLimits[hostID]
		o.Hosts = append(o.Hosts, domain.Host{
			ID: hostID, Name: hostID, Provider: c.ProviderName(), Availability: "unavailable", UnavailableReason: "not_observed",
			WakeOnLAN: limit.WakeOnLAN != nil, Capacity: domain.HostCapacity{MemoryReserveBytes: limit.MemoryReserveBytes, MaxVMs: limit.MaxVMs},
		})
		o.StorageContainers = append(o.StorageContainers, domain.StorageContainer{
			ID: c.config.PoolID + "." + hostID, PoolID: c.config.PoolID, HostID: hostID, Provider: c.ProviderName(), Kind: "remote",
			Availability: "unavailable", UnavailableReason: "not_observed",
		})
	}
	return o
}

func sortedHostIDs(limits map[string]HostLimit) []string {
	ids := make([]string, 0, len(limits))
	for id := range limits {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

type worker struct {
	Name             string            `json:"name"`
	LastSeen         time.Time         `json:"last_seen"`
	SchedulingPaused bool              `json:"scheduling_paused"`
	Resources        map[string]uint64 `json:"resources"`
	Labels           map[string]string `json:"labels"`
	Arch             string            `json:"arch"`
	Runtime          string            `json:"runtime"`
}

type vm struct {
	Name           string            `json:"name"`
	Image          string            `json:"image,omitempty"`
	CPU            uint64            `json:"cpu,omitempty"`
	Memory         uint64            `json:"memory,omitempty"`
	DiskSize       uint64            `json:"diskSize,omitempty"`
	Headless       bool              `json:"headless,omitempty"`
	OS             string            `json:"os,omitempty"`
	Arch           string            `json:"arch,omitempty"`
	Runtime        string            `json:"runtime,omitempty"`
	PowerState     string            `json:"powerState,omitempty"`
	Status         string            `json:"status,omitempty"`
	Worker         string            `json:"worker,omitempty"`
	AssignedCPU    uint64            `json:"assignedCPU,omitempty"`
	AssignedMemory uint64            `json:"assignedMemory,omitempty"`
	RandomSerial   bool              `json:"randomSerial,omitempty"`
	Resources      map[string]uint64 `json:"resources,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

func (c *Client) Observe(ctx context.Context) (domain.Observation, error) {
	now := c.now()
	var workers []worker
	if err := c.request(ctx, http.MethodGet, "workers", nil, &workers); err != nil {
		return domain.Observation{}, err
	}
	var vms []vm
	if err := c.request(ctx, http.MethodGet, "vms", nil, &vms); err != nil {
		return domain.Observation{}, err
	}
	o := domain.Observation{VMs: []domain.VM{}, Provider: c.ProviderName(), HostMax: c.config.MaxVMs, At: now}
	onlineWorkers := map[string]bool{}
	poolWorkers := map[string]bool{}
	type hostUsage struct {
		resources domain.Resources
		vms       int
	}
	usage := map[string]hostUsage{}
	for _, raw := range vms {
		state := orchardState(raw)
		if raw.Worker == "" || state == "stopped" || state == "failed" {
			continue
		}
		cpu := raw.AssignedCPU
		if cpu == 0 {
			cpu = raw.CPU
		}
		memory := raw.AssignedMemory
		if memory == 0 {
			memory = raw.Memory
		}
		current := usage[raw.Worker]
		current.vms++
		current.resources.CPU += int(cpu)
		current.resources.MemoryBytes += int64(memory) << 20
		current.resources.DiskBytes += int64(raw.DiskSize) * 1_000_000_000
		usage[raw.Worker] = current
	}
	unsafeCapacity := false
	for _, raw := range workers {
		if !domain.ValidName(raw.Name) || !matchesLabels(raw.Labels, c.config.WorkerLabels) {
			continue
		}
		poolWorkers[raw.Name] = true
		offline := raw.LastSeen.IsZero() || now.Sub(raw.LastSeen) > time.Duration(c.config.WorkerOfflineAfterSeconds)*time.Second
		availability := "available"
		reason := ""
		if offline {
			availability, reason = "unavailable", "host_offline"
		} else if raw.SchedulingPaused {
			availability, reason = "unavailable", "scheduling_paused"
		} else {
			onlineWorkers[raw.Name] = true
		}
		limit, hasLimit := c.config.HostLimits[raw.Name]
		memoryAllocatable := int64(raw.Resources["org.cirruslabs.memory-mib"]) << 20
		memoryTotal := memoryAllocatable
		maxVMs := int(raw.Resources["org.cirruslabs.tart-vms"])
		if raw.Runtime != "tart" {
			maxVMs = int(raw.Resources["ai.virfield.vm-slots"])
		}
		if maxVMs == 0 {
			maxVMs = c.config.MaxVMs
		}
		if hasLimit {
			memoryTotal = int64(raw.Resources["ai.virfield.physical-memory-mib"]) << 20
			expectedAllocatable := memoryTotal - limit.MemoryReserveBytes
			advertisedSlots := int(raw.Resources["ai.virfield.vm-slots"])
			if raw.Runtime == "tart" {
				advertisedSlots = int(raw.Resources["org.cirruslabs.tart-vms"])
			}
			if memoryTotal <= limit.MemoryReserveBytes || memoryAllocatable > expectedAllocatable || advertisedSlots < 1 || advertisedSlots > limit.MaxVMs {
				availability, reason = "unavailable", "capacity_policy_mismatch"
				delete(onlineWorkers, raw.Name)
				unsafeCapacity = true
			}
			maxVMs = min(limit.MaxVMs, advertisedSlots)
		} else if len(c.config.HostLimits) != 0 {
			availability, reason = "unavailable", "capacity_policy_missing"
			delete(onlineWorkers, raw.Name)
			unsafeCapacity = true
		}
		used := usage[raw.Name]
		capacity := domain.HostCapacity{
			Total:       domain.Resources{CPU: int(raw.Resources["org.cirruslabs.logical-cores"]), MemoryBytes: memoryTotal},
			Allocatable: domain.Resources{CPU: int(raw.Resources["org.cirruslabs.logical-cores"]), MemoryBytes: memoryAllocatable},
			Allocated:   used.resources, MemoryReserveBytes: limit.MemoryReserveBytes, MaxVMs: maxVMs, UsedVMs: used.vms,
		}
		capacity.Available = subtractResources(capacity.Allocatable, capacity.Allocated)
		hostOS := "macOS"
		if raw.Runtime == "vetu" {
			hostOS = "linux"
		}
		o.Hosts = append(o.Hosts, domain.Host{ID: raw.Name, Name: raw.Name, Provider: c.ProviderName(), OS: hostOS, Arch: raw.Arch, Availability: availability, UnavailableReason: reason, LastSeenAt: raw.LastSeen, WakeOnLAN: limit.WakeOnLAN != nil, Capacity: capacity})
		o.StorageContainers = append(o.StorageContainers, domain.StorageContainer{
			ID: c.config.PoolID + "." + raw.Name, PoolID: c.config.PoolID, HostID: raw.Name,
			Provider: c.ProviderName(), Kind: "remote", Availability: availability,
			UnavailableReason: reason, LastSeenAt: raw.LastSeen,
		})
	}
	for _, hostID := range sortedHostIDs(c.config.HostLimits) {
		limit := c.config.HostLimits[hostID]
		if poolWorkers[hostID] {
			continue
		}
		o.Hosts = append(o.Hosts, domain.Host{
			ID: hostID, Name: hostID, Provider: c.ProviderName(), Availability: "unavailable", UnavailableReason: "host_unobserved",
			WakeOnLAN: limit.WakeOnLAN != nil, Capacity: domain.HostCapacity{MemoryReserveBytes: limit.MemoryReserveBytes, MaxVMs: limit.MaxVMs},
		})
		o.StorageContainers = append(o.StorageContainers, domain.StorageContainer{
			ID: c.config.PoolID + "." + hostID, PoolID: c.config.PoolID, HostID: hostID, Provider: c.ProviderName(), Kind: "remote",
			Availability: "unavailable", UnavailableReason: "host_unobserved",
		})
	}
	for _, raw := range vms {
		if !domain.ValidName(raw.Name) || raw.Status == "" {
			return domain.Observation{}, errors.New("invalid Orchard VM inventory")
		}
		if raw.Worker != "" && !poolWorkers[raw.Worker] {
			// A VM assigned outside this configured pool is not ours to expose.
			continue
		}
		state := orchardState(raw)
		cpu := raw.AssignedCPU
		if cpu == 0 {
			cpu = raw.CPU
		}
		memory := raw.AssignedMemory
		if memory == 0 {
			memory = raw.Memory
		}
		observed := domain.VM{
			Resources: domain.Resources{CPU: int(cpu), MemoryBytes: int64(memory) << 20, DiskBytes: int64(raw.DiskSize) * 1_000_000_000},
			Name:      raw.Name, Location: c.config.PoolID, Provider: c.ProviderName(), HostID: raw.Worker,
			ContainerID: c.config.PoolID + "." + raw.Worker, OS: normalizeOS(raw.OS), State: state, LastSeenAt: now,
		}
		if raw.Worker == "" || !onlineWorkers[raw.Worker] {
			observed.UnavailableReason = "host_offline"
		}
		if state == "running" {
			var ipResponse struct {
				IP string `json:"ip"`
			}
			if err := c.request(ctx, http.MethodGet, "vms/"+url.PathEscape(raw.Name)+"/ip?wait=0", nil, &ipResponse); err == nil && net.ParseIP(ipResponse.IP) != nil {
				observed.IP = ipResponse.IP
				observed.SSHAvailable = true
			}
		}
		o.VMs = append(o.VMs, observed)
		if state == "running" || state == "starting" {
			o.HostUsed++
		}
	}
	if len(onlineWorkers) == 0 || unsafeCapacity {
		o.HostUsed = o.HostMax
	}
	return o, nil
}

func subtractResources(total, used domain.Resources) domain.Resources {
	return domain.Resources{CPU: max(0, total.CPU-used.CPU), MemoryBytes: max(0, total.MemoryBytes-used.MemoryBytes), DiskBytes: max(0, total.DiskBytes-used.DiskBytes)}
}

func matchesLabels(actual, wanted map[string]string) bool {
	for key, value := range wanted {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func orchardState(raw vm) string {
	if raw.PowerState == "stopped" || raw.PowerState == "suspended" {
		return "stopped"
	}
	switch raw.Status {
	case "pending":
		return "starting"
	case "running":
		return "running"
	case "failed":
		return "failed"
	default:
		return raw.Status
	}
}

func normalizeOS(value string) string {
	if value == "" || value == "darwin" || strings.EqualFold(value, "macos") {
		return "macOS"
	}
	return value
}

func (c *Client) Clone(ctx context.Context, template domain.Template, lease domain.Lease) error {
	if template.Source == "" {
		return &RejectedMutation{Status: http.StatusPreconditionFailed}
	}
	runtimeName := "tart"
	if template.Arch == "amd64" || template.OS == "linux" {
		runtimeName = "vetu"
	}
	resources := map[string]uint64{
		"org.cirruslabs.logical-cores": uint64(template.Resources.CPU),
		"org.cirruslabs.memory-mib":    uint64(template.Resources.MemoryBytes >> 20),
	}
	if template.OS == "macOS" && runtimeName == "tart" {
		resources["org.cirruslabs.tart-vms"] = 1
	}
	if len(c.config.HostLimits) != 0 && runtimeName == "vetu" {
		resources["ai.virfield.vm-slots"] = 1
	}
	body := vm{
		Name: lease.VMName, Image: template.Source, CPU: uint64(template.Resources.CPU),
		Memory: uint64(template.Resources.MemoryBytes >> 20), DiskSize: uint64((template.Resources.DiskBytes + 999_999_999) / 1_000_000_000),
		Headless: true, OS: mapOS(template.OS), Arch: template.Arch, Runtime: runtimeName,
		PowerState: "running", RandomSerial: template.OS == "macOS", Resources: resources,
		Labels: copyLabels(c.config.WorkerLabels),
	}
	return c.request(ctx, http.MethodPost, "vms", body, nil)
}

func mapOS(value string) string {
	if value == "macOS" {
		return "darwin"
	}
	return value
}

func copyLabels(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (c *Client) Start(context.Context, domain.Lease) error { return nil }

func (c *Client) Stop(ctx context.Context, lease domain.Lease) error {
	var current vm
	if err := c.request(ctx, http.MethodGet, "vms/"+url.PathEscape(lease.VMName), nil, &current); err != nil {
		return err
	}
	current.PowerState = "stopped"
	return c.request(ctx, http.MethodPut, "vms/"+url.PathEscape(lease.VMName), current, nil)
}

func (c *Client) Delete(ctx context.Context, lease domain.Lease) error {
	return c.request(ctx, http.MethodDelete, "vms/"+url.PathEscape(lease.VMName), nil, nil)
}

func (c *Client) WakeHost(ctx context.Context, hostID string) error {
	limit, ok := c.config.HostLimits[hostID]
	if !ok || limit.WakeOnLAN == nil {
		return errors.New("Wake-on-LAN is not configured for host")
	}
	return wol.Send(ctx, limit.WakeOnLAN.MACAddress, limit.WakeOnLAN.BroadcastAddress)
}

func (c *Client) request(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	u := *c.base
	query := ""
	if before, after, ok := strings.Cut(path, "?"); ok {
		path, query = before, after
	}
	u.Path = strings.TrimRight(c.base.Path, "/") + "/v1/" + path
	u.RawQuery = query
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(strings.SplitN(c.auth, ":", 2)[0], strings.SplitN(c.auth, ":", 2)[1])
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("orchard %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusPreconditionFailed, http.StatusUnprocessableEntity:
			return &RejectedMutation{Status: resp.StatusCode}
		default:
			return fmt.Errorf("orchard %s returned HTTP %d", method, resp.StatusCode)
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil {
		return err
	}
	if len(data) > responseLimit {
		return errors.New("Orchard response exceeds 4 MiB")
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			return errors.New("invalid Orchard response")
		}
	}
	return nil
}
