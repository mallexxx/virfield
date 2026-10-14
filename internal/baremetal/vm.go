package baremetal

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// VMConfig runs disposable macOS VMs as rootless Podman containers on a Linux
// node; it mirrors config.NodeVMs.
type VMConfig struct {
	Image, GoldensDir, DiskSize  string
	StorageDir                   string
	PortBase, Max, CPU, MemoryGB int
	ResourceLimits               *domain.ResourceLimits
	CacheLimitBytes              int64
}

// vmRecord is one VM folder on a node: a lease clone (scope leases) or an
// image-securing clone (scope images).
type vmRecord struct {
	Scope  string `json:"scope"`
	State  string `json:"state"`
	Golden string `json:"golden"`
	Port   int    `json:"port"`
	// SSH is true once the guest's SSH banner answers on Port.
	SSH       bool             `json:"ssh"`
	Resources domain.Resources `json:"resources,omitempty"`
}

// vmGolden reports whether name is a VM golden on the node's last probe.
func (r *Runtime) vmGolden(n Node, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return n.VMs != nil && r.last[n.ID].goldens[name]
}

func vmLeaseName(l domain.Lease) error {
	if !strings.HasPrefix(l.VMName, "vf-") || !domain.ValidName(l.VMName) {
		return domain.Err("invalid_request", "Not a disposable lease name")
	}
	return nil
}

// cloneVM clones a secured golden. A golden committed after the last probe
// is found by probing again, never by falling back to a host lease.
func (r *Runtime) cloneVM(ctx context.Context, n Node, t domain.Template, l domain.Lease) error {
	if err := vmLeaseName(l); err != nil {
		return err
	}
	if n.VMs == nil {
		return domain.Err("invalid_request", "Node "+n.ID+" has no VMs configured")
	}
	if err := r.checkVMResources(ctx, n, l, t.Name, false); err != nil {
		return err
	}
	if !r.vmGolden(n, t.Name) {
		p, err := r.probe(ctx, n, r.now())
		if err != nil {
			return err
		}
		r.mu.Lock()
		r.last[n.ID] = p
		r.mu.Unlock()
		if !p.goldens[t.Name] {
			return domain.Err("template_unavailable", "Golden "+t.Name+" is not on node "+n.ID)
		}
	}
	v := n.VMs
	_, err := r.runOn(ctx, n, command(vmCloneScript, "leases", l.VMName, t.Name, v.GoldensDir, v.Image, strconv.Itoa(v.PortBase), strconv.Itoa(v.Max)))
	return err
}

// DesktopPortOffset places a lease's Screen Sharing on the node loopback at
// its SSH port plus this offset.
const DesktopPortOffset = 1000

// startVM publishes guest SSH on publish and, when desktop is set, the
// guest's Screen Sharing on the node loopback.
func (r *Runtime) startVM(ctx context.Context, n Node, scope, name, publish string, desktop bool) error {
	v := n.VMs
	desktopAddress := ""
	if desktop {
		port, err := r.vmPort(ctx, n, scope, name)
		if err != nil {
			return err
		}
		desktopAddress = "127.0.0.1:" + strconv.Itoa(port+DesktopPortOffset)
	}
	_, err := r.runOn(ctx, n, command(vmStartScript, scope, name, v.GoldensDir, v.Image, publish, strconv.Itoa(v.CPU), strconv.Itoa(v.MemoryGB), v.DiskSize, n.KeepAwakeFile, desktopAddress))
	return err
}

// vmPort reads a clone's SSH port from the node.
func (r *Runtime) vmPort(ctx context.Context, n Node, scope, name string) (int, error) {
	out, err := r.runOn(ctx, n, command(`cat "${VF_VM_HOME:-$HOME/virfield}/$1/$2/port"`, scope, name))
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || port < 1024 || port > 65535-DesktopPortOffset {
		return 0, domain.Err("invalid_request", "Node VM has no valid SSH port")
	}
	return port, nil
}

// DialService reaches a node VM lease's desktop: the guest's Screen Sharing
// published on the node loopback, through the pinned node connection.
func (r *Runtime) DialService(ctx context.Context, l domain.Lease, service string) (net.Conn, error) {
	if service != "desktop" || l.ImageID == "" || l.SSHPort < 1 {
		return nil, domain.Err("invalid_request", "Only node VM leases have a desktop")
	}
	desktop := l
	desktop.SSHPort = l.SSHPort + DesktopPortOffset
	return r.DialGuest(ctx, desktop, "")
}

func (r *Runtime) stopVM(ctx context.Context, n Node, scope, name string, remove bool) error {
	script := vmStopScript
	if remove {
		script = vmDeleteScript
	}
	_, err := r.runOn(ctx, n, command(script, scope, name, n.KeepAwakeFile))
	return err
}

func vmObservations(n Node, p probe, reachable bool, used *int) []domain.VM {
	vms := []domain.VM{}
	for name := range p.goldens {
		resources := vmResources(n)
		resources.DiskBytes = p.goldenDisks[name]
		vms = append(vms, domain.VM{Name: name, Location: n.ID, Provider: providerName, HostID: n.ID, ContainerID: n.ID, OS: "macOS", State: "stopped", LastSeenAt: p.at, Resources: resources})
	}
	for name, rec := range p.vms {
		vm := domain.VM{Name: name, Location: n.ID, Provider: providerName, HostID: n.ID, ContainerID: n.ID, OS: "macOS", State: rec.State, SSHPort: rec.Port, LastSeenAt: p.at}
		vm.Resources = rec.Resources
		if rec.State == "running" {
			*used++
			if reachable {
				vm.IP = p.ip
				vm.SSHAvailable = rec.SSH
			}
		}
		vms = append(vms, vm)
	}
	return vms
}

// parseVMLine reads "vm=<scope> <name> <state> <port> <golden> <ssh 0|1>".
func parseVMLine(value string) (string, vmRecord, bool) {
	f := strings.Fields(value)
	if (len(f) != 6 && len(f) != 9) || (f[5] != "0" && f[5] != "1") || (f[0] != "leases" && f[0] != "images") || !domain.ValidName(f[1]) || (f[2] != "running" && f[2] != "stopped") || !domain.ValidName(f[4]) {
		return "", vmRecord{}, false
	}
	port, err := strconv.Atoi(f[3])
	if err != nil || port < 1024 || port > 65535 {
		return "", vmRecord{}, false
	}
	record := vmRecord{Scope: f[0], State: f[2], Port: port, Golden: f[4], SSH: f[5] == "1"}
	if len(f) == 9 {
		record.Resources.CPU, _ = strconv.Atoi(f[6])
		record.Resources.MemoryBytes, _ = strconv.ParseInt(f[7], 10, 64)
		record.Resources.DiskBytes, _ = strconv.ParseInt(f[8], 10, 64)
	}
	return f[1], record, true
}

func vmResources(n Node) domain.Resources {
	if n.VMs == nil {
		return domain.Resources{}
	}
	size, _ := strconv.ParseInt(strings.TrimSuffix(n.VMs.DiskSize, "G"), 10, 64)
	return domain.Resources{CPU: n.VMs.CPU, MemoryBytes: int64(n.VMs.MemoryGB) << 30, DiskBytes: size << 30}
}

// Guests prepares node VM leases with the image pipeline's credentials.
type Guests interface {
	Prepare(context.Context, domain.Lease) (domain.SSHConnection, error)
	LeasePassword(domain.Lease) (string, error)
	Forget(domain.Lease) error
}

// LeasePassword is a node VM lease's guest password; host leases have none.
func (r *Runtime) LeasePassword(l domain.Lease) (string, error) {
	if l.ImageID == "" || r.guests == nil {
		return "", domain.Err("lease_unavailable", "Only node VM leases have a guest password")
	}
	return r.guests.LeasePassword(l)
}

func (r *Runtime) SetGuests(g Guests) { r.guests = g }

// Forget removes a node VM lease's private credentials after deletion; host
// leases keep none.
func (r *Runtime) Forget(l domain.Lease) error {
	if l.ImageID == "" || r.guests == nil {
		return nil
	}
	return r.guests.Forget(l)
}

// DialGuest reaches a node VM's published SSH port from the node itself, so
// Virfield's guest SSH never depends on the port being published on the LAN.
func (r *Runtime) DialGuest(ctx context.Context, l domain.Lease, _ string) (net.Conn, error) {
	n, err := r.node(l.Location)
	if err != nil {
		return nil, err
	}
	if l.SSHPort < 1 {
		return nil, domain.Err("guest_connect_failed", "Node VM has no SSH port")
	}
	s, err := r.dial(ctx, n)
	if err != nil {
		return nil, err
	}
	conn, err := s.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.SSHPort)))
	if err != nil {
		s.Close()
		return nil, err
	}
	return &tunnelConn{Conn: conn, session: s}, nil
}

// tunnelConn closes the node session with the forwarded connection.
type tunnelConn struct {
	net.Conn
	session session
}

func (c *tunnelConn) Close() error {
	err := c.Conn.Close()
	c.session.Close()
	return err
}

// imageNode validates an image VM name and returns its node. Image VMs live
// in their own scope and never take a lease name.
func (r *Runtime) imageNode(l domain.Lease) (Node, error) {
	if !domain.ValidName(l.VMName) || strings.HasPrefix(l.VMName, "vf-") {
		return Node{}, domain.Err("invalid_request", "Not an image name")
	}
	n, err := r.node(l.Location)
	if err != nil {
		return Node{}, err
	}
	if n.VMs == nil {
		return Node{}, domain.Err("invalid_request", "Node "+n.ID+" has no VMs configured")
	}
	return n, nil
}

// CloneImage clones a bootstrap golden for securing. Image VMs use the ports
// after the lease range.
func (r *Runtime) CloneImage(ctx context.Context, l domain.Lease, golden string) error {
	n, err := r.imageNode(l)
	if err != nil {
		return err
	}
	if !domain.ValidName(golden) {
		return domain.Err("invalid_request", "Invalid golden name")
	}
	if err := r.ensureAwake(ctx, n); err != nil {
		return err
	}
	if err := r.checkVMResources(ctx, n, l, golden, true); err != nil {
		return err
	}
	v := n.VMs
	_, err = r.runOn(ctx, n, command(vmCloneScript, "images", l.VMName, golden, v.GoldensDir, v.Image, strconv.Itoa(v.PortBase+v.Max), strconv.Itoa(v.Max)))
	return err
}

// StartImage publishes the image VM's SSH on the node loopback only: its
// bootstrap password is public until the image is secured.
func (r *Runtime) StartImage(ctx context.Context, l domain.Lease) error {
	n, err := r.imageNode(l)
	if err != nil {
		return err
	}
	if err := r.ensureAwake(ctx, n); err != nil {
		return err
	}
	if err := r.checkVMResources(ctx, n, l, "", true); err != nil {
		return err
	}
	return r.startVM(ctx, n, "images", l.VMName, "127.0.0.1", false)
}

func (r *Runtime) StopImage(ctx context.Context, l domain.Lease) error {
	n, err := r.imageNode(l)
	if err != nil {
		return err
	}
	return r.stopVM(ctx, n, "images", l.VMName, false)
}

func (r *Runtime) DeleteImage(ctx context.Context, l domain.Lease) error {
	n, err := r.imageNode(l)
	if err != nil {
		return err
	}
	_, err = r.runOn(ctx, n, command(vmImageDeleteScript, "images", l.VMName, n.VMs.GoldensDir, n.KeepAwakeFile))
	return err
}

// CommitImage writes the secured clone as a read-only golden named after the
// image and removes the clone.
func (r *Runtime) CommitImage(ctx context.Context, l domain.Lease) error {
	n, err := r.imageNode(l)
	if err != nil {
		return err
	}
	if err := r.checkVMResources(ctx, n, l, "", true); err != nil {
		return err
	}
	_, err = r.runOn(ctx, n, command(vmCommitScript, "images", l.VMName, n.VMs.GoldensDir, n.VMs.Image))
	return err
}

// HasVMs reports whether any node runs macOS VMs.
func (r *Runtime) HasVMs() bool {
	for _, n := range r.nodes {
		if n.VMs != nil {
			return true
		}
	}
	return false
}
