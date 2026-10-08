// Package baremetal leases physical hosts over pinned SSH as one unprivileged
// account. Lease state lives on the node (lease markers), so it survives a
// controller restart like a VM directory does. Physical hosts are not
// disposable: release revokes access and removes the lease workspace only.
package baremetal

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/wol"
)

const providerName = "baremetal"

// Node is one physical host. KeepAwakeFile is an optional absolute path that
// the node's own sleep guard honors while it exists.
type Node struct {
	ID, Name, Address, User, HostKey, OS, Arch string
	KeepAwakeFile                              string
	WakeMAC, WakeBroadcast                     string
}

type session interface {
	Run(ctx context.Context, command, input string) (string, error)
	Close()
}

type dialer func(ctx context.Context, n Node) (session, error)

// probe is the last successful observation of one node.
type probe struct {
	golden domain.VM
	host   domain.Host
	leases map[string]string
	ip     string
	at     time.Time
}

type Runtime struct {
	nodes        []Node
	dial         dialer
	now          func() time.Time
	probeTimeout time.Duration
	wakeTimeout  time.Duration
	wakePoll     time.Duration
	mu           sync.Mutex
	last         map[string]probe
}

func New(nodes []Node, dial dialer) *Runtime {
	return &Runtime{nodes: nodes, dial: dial, now: func() time.Time { return time.Now().UTC() }, probeTimeout: 10 * time.Second, wakeTimeout: 3 * time.Minute, wakePoll: 5 * time.Second, last: map[string]probe{}}
}

func (r *Runtime) ProviderName() string { return providerName }

// ConfiguredInventory keeps configured nodes visible before the first probe.
func (r *Runtime) ConfiguredInventory() domain.Observation {
	o := domain.Observation{Provider: providerName, VMs: []domain.VM{}, HostMax: len(r.nodes)}
	for _, n := range r.nodes {
		o.Hosts = append(o.Hosts, r.host(n, "unavailable", "not_observed", domain.Resources{}))
		o.StorageContainers = append(o.StorageContainers, container(n, "unavailable", "not_observed", time.Time{}))
	}
	return o
}

// Observe probes every node concurrently. An unreachable node is reported as
// an unavailable host, never as an inventory failure.
func (r *Runtime) Observe(ctx context.Context) (domain.Observation, error) {
	now := r.now()
	results := make([]probe, len(r.nodes))
	errs := make([]error, len(r.nodes))
	var wg sync.WaitGroup
	for i, n := range r.nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = r.probe(ctx, n, now)
		}()
	}
	wg.Wait()
	o := domain.Observation{Provider: providerName, VMs: []domain.VM{}, HostMax: len(r.nodes), At: now}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, n := range r.nodes {
		if errs[i] == nil {
			r.last[n.ID] = results[i]
			o.Hosts = append(o.Hosts, results[i].host)
			o.StorageContainers = append(o.StorageContainers, container(n, "available", "", now))
			o.VMs = append(o.VMs, results[i].golden)
			o.VMs = append(o.VMs, leaseVMs(n, results[i], true, &o.HostUsed)...)
			continue
		}
		// Offline: keep known leases visible without SSH. A node that can be
		// woken keeps its last golden, so it can be admitted while asleep.
		cached, known := r.last[n.ID]
		reason := "offline"
		if n.WakeMAC != "" {
			reason = "asleep"
		}
		h := r.host(n, "unavailable", reason, cached.host.Capacity.Total)
		o.Hosts = append(o.Hosts, h)
		if known && n.WakeMAC != "" {
			o.StorageContainers = append(o.StorageContainers, container(n, "available", "", cached.at))
			o.VMs = append(o.VMs, cached.golden)
		} else {
			o.StorageContainers = append(o.StorageContainers, container(n, "unavailable", reason, cached.at))
		}
		if known {
			o.VMs = append(o.VMs, leaseVMs(n, cached, false, &o.HostUsed)...)
		}
	}
	return o, nil
}

func (r *Runtime) probe(ctx context.Context, n Node, now time.Time) (probe, error) {
	ctx, cancel := context.WithTimeout(ctx, r.probeTimeout)
	defer cancel()
	s, err := r.dial(ctx, n)
	if err != nil {
		return probe{}, err
	}
	defer s.Close()
	out, err := s.Run(ctx, command(probeScript), "")
	if err != nil {
		return probe{}, err
	}
	p, err := parseProbe(out)
	if err != nil {
		return probe{}, err
	}
	ip, err := resolve(ctx, n.Address)
	if err != nil {
		return probe{}, err
	}
	resources := domain.Resources{CPU: p.cpu, MemoryBytes: p.memory}
	h := r.host(n, "available", "", resources)
	h.LastSeenAt = now
	golden := domain.VM{Name: p.volume, Location: n.ID, Provider: providerName, HostID: n.ID, ContainerID: n.ID, OS: n.OS, State: "stopped", Resources: resources, LastSeenAt: now}
	return probe{golden: golden, host: h, leases: p.leases, ip: ip, at: now}, nil
}

func (r *Runtime) host(n Node, availability, reason string, total domain.Resources) domain.Host {
	name := n.Name
	if name == "" {
		name = n.ID
	}
	return domain.Host{ID: n.ID, Name: name, Provider: providerName, OS: n.OS, Arch: n.Arch, Availability: availability, UnavailableReason: reason, WakeOnLAN: n.WakeMAC != "", Capacity: domain.HostCapacity{Total: total, Allocatable: total, MaxVMs: 1}}
}

func container(n Node, availability, reason string, seen time.Time) domain.StorageContainer {
	return domain.StorageContainer{ID: n.ID, HostID: n.ID, Provider: providerName, Kind: "node", Availability: availability, UnavailableReason: reason, LastSeenAt: seen}
}

func leaseVMs(n Node, p probe, reachable bool, used *int) []domain.VM {
	vms := []domain.VM{}
	for name, state := range p.leases {
		vm := domain.VM{Name: name, Location: n.ID, Provider: providerName, HostID: n.ID, ContainerID: n.ID, OS: n.OS, State: state, LastSeenAt: p.at}
		if state == "running" {
			*used++
			if reachable {
				vm.IP = p.ip
				vm.SSHAvailable = true
			}
		}
		vms = append(vms, vm)
	}
	return vms
}

func (r *Runtime) Clone(ctx context.Context, _ domain.Template, l domain.Lease) error {
	return r.runLease(ctx, l, cloneScript)
}

// Start wakes a sleeping node when it can, then keeps it awake for the lease.
func (r *Runtime) Start(ctx context.Context, l domain.Lease) error {
	n, err := r.node(l.Location)
	if err != nil {
		return err
	}
	if err := r.ensureAwake(ctx, n); err != nil {
		return err
	}
	return r.runLease(ctx, l, startScript)
}

func (r *Runtime) Stop(ctx context.Context, l domain.Lease) error {
	return r.runLease(ctx, l, stopScript)
}

func (r *Runtime) Delete(ctx context.Context, l domain.Lease) error {
	return r.runLease(ctx, l, deleteScript)
}

// WakeHost sends the node's configured magic packet.
func (r *Runtime) WakeHost(ctx context.Context, hostID string) error {
	n, err := r.node(hostID)
	if err != nil {
		return err
	}
	if n.WakeMAC == "" {
		return errors.New("Wake-on-LAN is not configured for node")
	}
	return wol.Send(ctx, n.WakeMAC, n.WakeBroadcast)
}

func (r *Runtime) ensureAwake(ctx context.Context, n Node) error {
	s, err := r.dial(ctx, n)
	if err == nil {
		s.Close()
		return nil
	}
	if n.WakeMAC == "" {
		return domain.Err("backend_unavailable", "Node "+n.ID+" is offline and has no Wake-on-LAN")
	}
	if err := wol.Send(ctx, n.WakeMAC, n.WakeBroadcast); err != nil {
		return domain.Err("wake_failed", "Wake-on-LAN packet could not be sent")
	}
	deadline := time.Now().Add(r.wakeTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.wakePoll):
		}
		if s, err := r.dial(ctx, n); err == nil {
			s.Close()
			return nil
		}
	}
	return domain.Err("wake_failed", "Node "+n.ID+" did not wake before its deadline")
}

func (r *Runtime) runLease(ctx context.Context, l domain.Lease, script string) error {
	if !strings.HasPrefix(l.VMName, "vf-") || !domain.ValidName(l.VMName) {
		return domain.Err("invalid_request", "Not a disposable lease name")
	}
	n, err := r.node(l.Location)
	if err != nil {
		return err
	}
	s, err := r.dial(ctx, n)
	if err != nil {
		return err
	}
	defer s.Close()
	_, err = s.Run(ctx, command(script, l.VMName, n.KeepAwakeFile), "")
	return err
}

func (r *Runtime) node(id string) (Node, error) {
	for _, n := range r.nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return Node{}, domain.Err("not_found", "Unknown node "+id)
}

// command builds `/bin/sh -c <script> sh <args...>` with every word quoted.
func command(script string, args ...string) string {
	words := []string{"/bin/sh", "-c", quote(script), "sh"}
	for _, a := range args {
		words = append(words, quote(a))
	}
	return strings.Join(words, " ")
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

type probeResult struct {
	volume string
	cpu    int
	memory int64
	leases map[string]string
}

var invalidName = regexp.MustCompile(`[^a-zA-Z0-9_.]+`)

// normalizeVolume maps a boot volume name ("Big Sur") to a template name
// ("Big-Sur").
func normalizeVolume(v string) string {
	return strings.Trim(invalidName.ReplaceAllString(strings.TrimSpace(v), "-"), "-.")
}

func parseProbe(out string) (probeResult, error) {
	p := probeResult{leases: map[string]string{}}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "volume":
			p.volume = normalizeVolume(value)
		case "cpu":
			p.cpu, _ = strconv.Atoi(value)
		case "memory":
			p.memory, _ = strconv.ParseInt(value, 10, 64)
		case "lease":
			name, state, _ := strings.Cut(value, " ")
			if strings.HasPrefix(name, "vf-") && domain.ValidName(name) && (state == "running" || state == "stopped") {
				p.leases[name] = state
			}
		}
	}
	if !domain.ValidName(p.volume) || p.cpu < 1 || p.memory < 1 {
		return p, errors.New("node probe returned no usable boot environment")
	}
	return p, nil
}

// resolve returns the node's IPv4 address; the controller requires an IP.
func resolve(ctx context.Context, address string) (string, error) {
	if ip := net.ParseIP(address); ip != nil {
		return ip.String(), nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", address)
	if err != nil || len(ips) == 0 {
		return "", errors.New("node address does not resolve")
	}
	return ips[0].String(), nil
}
