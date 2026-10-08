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

// Node is one physical host. HostKeys are every identity the address may
// present, one per bootable system. KeepAwakeFile is an optional absolute
// path that the node's own sleep guard honors while it exists.
type Node struct {
	ID, Name, Address, User, OS, Arch string
	HostKeys                          []string
	KeepAwakeFile                     string
	WakeMAC, WakeBroadcast            string
	VMs                               *VMConfig
}

type session interface {
	Run(ctx context.Context, command, input string) (string, error)
	// HostKey is the identity the node presented, in authorized-keys format.
	HostKey() string
	Close()
}

type dialer func(ctx context.Context, n Node) (session, error)

// probe is the last successful observation of one node.
type probe struct {
	golden  domain.VM
	host    domain.Host
	leases  map[string]string
	goldens map[string]bool
	vms     map[string]vmRecord
	ip      string
	at      time.Time
}

type Runtime struct {
	nodes        []Node
	dial         dialer
	now          func() time.Time
	probeTimeout time.Duration
	wakeTimeout  time.Duration
	wakePoll     time.Duration
	// offlineBackoff spaces probes of an unreachable node, so a sleeping node
	// does not add its dial timeout to every inventory refresh.
	offlineBackoff time.Duration
	// probeInterval spaces probes of an online node: each probe is an SSH
	// session, and a node's sleep guard may hold it awake for SSH sessions.
	probeInterval time.Duration
	mu            sync.Mutex
	last          map[string]probe
	failedAt      map[string]time.Time
	changed       map[string]bool
	persistDir    string
}

func New(nodes []Node, dial dialer) *Runtime {
	return &Runtime{nodes: nodes, dial: dial, now: func() time.Time { return time.Now().UTC() }, probeTimeout: 10 * time.Second, wakeTimeout: 3 * time.Minute, wakePoll: 5 * time.Second, offlineBackoff: 30 * time.Second, probeInterval: time.Minute, last: map[string]probe{}, failedAt: map[string]time.Time{}, changed: map[string]bool{}}
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
	r.mu.Lock()
	skip := make([]bool, len(r.nodes))
	for i, n := range r.nodes {
		if failed, ok := r.failedAt[n.ID]; ok && now.Sub(failed) < r.offlineBackoff {
			skip[i] = true
			errs[i] = errors.New("node offline; probe deferred")
		} else if cached, ok := r.last[n.ID]; ok && !r.changed[n.ID] && now.Sub(cached.at) < r.probeInterval {
			skip[i] = true
			results[i] = cached
		} else {
			delete(r.changed, n.ID)
		}
	}
	r.mu.Unlock()
	var wg sync.WaitGroup
	for i, n := range r.nodes {
		if skip[i] {
			continue
		}
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
			delete(r.failedAt, n.ID)
			r.last[n.ID] = results[i]
			if !skip[i] {
				// Best effort: losing the record only costs asleep admission.
				_ = r.remember(n.ID, results[i])
			}
			h := results[i].host
			o.StorageContainers = append(o.StorageContainers, container(n, "available", "", now))
			o.VMs = append(o.VMs, results[i].golden)
			o.VMs = append(o.VMs, leaseVMs(n, results[i], true, &h.Capacity.UsedVMs)...)
			o.VMs = append(o.VMs, vmObservations(n, results[i], true, &h.Capacity.UsedVMs)...)
			o.HostUsed += h.Capacity.UsedVMs
			o.Hosts = append(o.Hosts, h)
			continue
		}
		// Offline: keep known leases visible without SSH. A node that can be
		// woken keeps its last golden, so it can be admitted while asleep.
		if !skip[i] {
			r.failedAt[n.ID] = now
		}
		cached, known := r.last[n.ID]
		reason := "offline"
		if n.WakeMAC != "" {
			reason = "asleep"
		}
		h := r.host(n, "unavailable", reason, cached.host.Capacity.Total)
		if known && n.WakeMAC != "" {
			o.StorageContainers = append(o.StorageContainers, container(n, "available", "", cached.at))
			o.VMs = append(o.VMs, cached.golden)
			for name := range cached.goldens {
				o.VMs = append(o.VMs, domain.VM{Name: name, Location: n.ID, Provider: providerName, HostID: n.ID, ContainerID: n.ID, OS: "macOS", State: "stopped", LastSeenAt: cached.at})
			}
		} else {
			o.StorageContainers = append(o.StorageContainers, container(n, "unavailable", reason, cached.at))
		}
		if known {
			o.VMs = append(o.VMs, leaseVMs(n, cached, false, &h.Capacity.UsedVMs)...)
			leased := cached
			leased.goldens = nil
			o.VMs = append(o.VMs, vmObservations(n, leased, false, &h.Capacity.UsedVMs)...)
		}
		o.HostUsed += h.Capacity.UsedVMs
		o.Hosts = append(o.Hosts, h)
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
	script := command(probeScript)
	if n.VMs != nil {
		script = command(probeScript+vmProbeLines, n.VMs.GoldensDir)
	}
	out, err := s.Run(ctx, script, "")
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
	return probe{golden: golden, host: h, leases: p.leases, goldens: p.goldens, vms: p.vms, ip: ip, at: now}, nil
}

func (r *Runtime) host(n Node, availability, reason string, total domain.Resources) domain.Host {
	name := n.Name
	if name == "" {
		name = n.ID
	}
	maxVMs := 1
	if n.VMs != nil {
		maxVMs += n.VMs.Max
	}
	return domain.Host{ID: n.ID, Name: name, Provider: providerName, OS: n.OS, Arch: n.Arch, Availability: availability, UnavailableReason: reason, WakeOnLAN: n.WakeMAC != "", Capacity: domain.HostCapacity{Total: total, Allocatable: total, MaxVMs: maxVMs}}
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

// Clone reserves the lease on the node. The controller clones before it
// starts, and a failed clone is quarantined, so a sleeping node is woken here.
func (r *Runtime) Clone(ctx context.Context, t domain.Template, l domain.Lease) error {
	n, err := r.node(l.Location)
	if err != nil {
		return err
	}
	if err := r.ensureAwake(ctx, n); err != nil {
		return err
	}
	if r.vmGolden(n, t.Name) {
		return r.cloneVM(ctx, n, t, l)
	}
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
	if r.vmLease(n, l.VMName) {
		return r.startVM(ctx, n, "leases", l.VMName, "0.0.0.0")
	}
	return r.runLease(ctx, l, startScript)
}

func (r *Runtime) Stop(ctx context.Context, l domain.Lease) error {
	return r.stopLease(ctx, l, false)
}

func (r *Runtime) Delete(ctx context.Context, l domain.Lease) error {
	return r.stopLease(ctx, l, true)
}

// stopLease runs the VM or host script. A clone not yet seen by a probe is
// recognised by the host script's refusal of a VM folder.
func (r *Runtime) stopLease(ctx context.Context, l domain.Lease, remove bool) error {
	n, err := r.node(l.Location)
	if err != nil {
		return err
	}
	vm := n.VMs != nil && r.vmLease(n, l.VMName)
	if !vm {
		script := stopScript
		if remove {
			script = deleteScript
		}
		out, err := r.runLeaseOut(ctx, l, script)
		if err == nil || n.VMs == nil || !strings.Contains(out, "node VM folder") {
			return err
		}
	}
	if !strings.HasPrefix(l.VMName, "vf-") || !domain.ValidName(l.VMName) {
		return domain.Err("invalid_request", "Not a disposable lease name")
	}
	return r.stopVM(ctx, n, "leases", l.VMName, remove)
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

// reached clears a node's offline backoff after any successful connection and
// marks it for a fresh probe, since a Virfield operation just changed it.
func (r *Runtime) reached(n Node) {
	r.mu.Lock()
	delete(r.failedAt, n.ID)
	r.changed[n.ID] = true
	r.mu.Unlock()
}

func (r *Runtime) ensureAwake(ctx context.Context, n Node) error {
	s, err := r.dial(ctx, n)
	if err == nil {
		s.Close()
		r.reached(n)
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
			r.reached(n)
			return nil
		}
	}
	return domain.Err("wake_failed", "Node "+n.ID+" did not wake before its deadline")
}

func (r *Runtime) runLease(ctx context.Context, l domain.Lease, script string) error {
	_, err := r.runLeaseOut(ctx, l, script)
	return err
}

func (r *Runtime) runLeaseOut(ctx context.Context, l domain.Lease, script string) (string, error) {
	if !strings.HasPrefix(l.VMName, "vf-") || !domain.ValidName(l.VMName) {
		return "", domain.Err("invalid_request", "Not a disposable lease name")
	}
	n, err := r.node(l.Location)
	if err != nil {
		return "", err
	}
	return r.runOn(ctx, n, command(script, l.VMName, n.KeepAwakeFile))
}

// runOn runs one fixed command on a node and marks the node for a re-probe.
func (r *Runtime) runOn(ctx context.Context, n Node, cmd string) (string, error) {
	s, err := r.dial(ctx, n)
	if err != nil {
		return "", err
	}
	defer s.Close()
	r.reached(n)
	return s.Run(ctx, cmd, "")
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
	volume  string
	cpu     int
	memory  int64
	leases  map[string]string
	goldens map[string]bool
	vms     map[string]vmRecord
}

var invalidName = regexp.MustCompile(`[^a-zA-Z0-9_.]+`)

// normalizeVolume maps a boot volume name ("Big Sur") to a template name
// ("Big-Sur").
func normalizeVolume(v string) string {
	return strings.Trim(invalidName.ReplaceAllString(strings.TrimSpace(v), "-"), "-.")
}

func parseProbe(out string) (probeResult, error) {
	p := probeResult{leases: map[string]string{}, goldens: map[string]bool{}, vms: map[string]vmRecord{}}
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
		case "golden":
			if domain.ValidName(value) {
				p.goldens[value] = true
			}
		case "vm":
			if name, rec, ok := parseVMLine(value); ok {
				p.vms[name] = rec
			}
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
