package baremetal

// VMConfig runs disposable macOS VMs as rootless Podman containers on a Linux
// node; it mirrors config.NodeVMs.
type VMConfig struct {
	Image, GoldensDir, DiskSize  string
	PortBase, Max, CPU, MemoryGB int
}
