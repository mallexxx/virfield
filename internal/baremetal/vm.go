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
	PortBase, Max, CPU, MemoryGB int
}

// vmRecord is one VM folder on a node: a lease clone (scope leases) or an
// image-securing clone (scope images).
type vmRecord struct {
	Scope  string `json:"scope"`
	State  string `json:"state"`
	Golden string `json:"golden"`
	Port   int    `json:"port"`
}

// vmGolden reports whether name is a VM golden on the node's last probe.
func (r *Runtime) vmGolden(n Node, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return n.VMs != nil && r.last[n.ID].goldens[name]
}

// vmLease reports whether the node's last probe saw name as a VM folder.
func (r *Runtime) vmLease(n Node, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.last[n.ID].vms[name]
	return n.VMs != nil && ok && rec.Scope == "leases"
}

func (r *Runtime) cloneVM(ctx context.Context, n Node, t domain.Template, l domain.Lease) error {
	// Bootstrap goldens keep a public password; only a secured image is leased.
	if l.ImageID == "" {
		return domain.Err("invalid_request", "Node VM leases require a verified image")
	}
	v := n.VMs
	_, err := r.runOn(ctx, n, command(vmCloneScript, "leases", l.VMName, t.Name, v.GoldensDir, v.Image, strconv.Itoa(v.PortBase), strconv.Itoa(v.Max)))
	return err
}

func (r *Runtime) startVM(ctx context.Context, n Node, scope, name, publish string) error {
	v := n.VMs
	_, err := r.runOn(ctx, n, command(vmStartScript, scope, name, v.GoldensDir, v.Image, publish, strconv.Itoa(v.CPU), strconv.Itoa(v.MemoryGB), v.DiskSize, n.KeepAwakeFile))
	return err
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
		vms = append(vms, domain.VM{Name: name, Location: n.ID, Provider: providerName, HostID: n.ID, ContainerID: n.ID, OS: "macOS", State: "stopped", LastSeenAt: p.at})
	}
	for name, rec := range p.vms {
		vm := domain.VM{Name: name, Location: n.ID, Provider: providerName, HostID: n.ID, ContainerID: n.ID, OS: "macOS", State: rec.State, SSHPort: rec.Port, LastSeenAt: p.at}
		if rec.State == "running" {
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

// parseVMLine reads "vm=<scope> <name> <state> <port> <golden>".
func parseVMLine(value string) (string, vmRecord, bool) {
	f := strings.Fields(value)
	if len(f) != 5 || (f[0] != "leases" && f[0] != "images") || !domain.ValidName(f[1]) || (f[2] != "running" && f[2] != "stopped") || !domain.ValidName(f[4]) {
		return "", vmRecord{}, false
	}
	port, err := strconv.Atoi(f[3])
	if err != nil || port < 1024 || port > 65535 {
		return "", vmRecord{}, false
	}
	return f[1], vmRecord{Scope: f[0], State: f[2], Port: port, Golden: f[4]}, true
}

// Guests prepares node VM leases with the image pipeline's credentials.
type Guests interface {
	Prepare(context.Context, domain.Lease) (domain.SSHConnection, error)
	Forget(domain.Lease) error
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
	return r.startVM(ctx, n, "images", l.VMName, "127.0.0.1")
}

func (r *Runtime) StopImage(ctx context.Context, l domain.Lease) error {
	n, err := r.imageNode(l)
	if err != nil {
		return err
	}
	return r.stopVM(ctx, n, "images", l.VMName, false)
}

// CommitImage writes the secured clone as a read-only golden named after the
// image and removes the clone.
func (r *Runtime) CommitImage(ctx context.Context, l domain.Lease) error {
	n, err := r.imageNode(l)
	if err != nil {
		return err
	}
	_, err = r.runOn(ctx, n, command(vmCommitScript, "images", l.VMName, n.VMs.GoldensDir, n.VMs.Image))
	return err
}
