# Virfield

Virfield manages macOS VM images, disposable VM leases and physical macOS/Linux
nodes. It tracks capacity, pins SSH identities, opens desktop tunnels and confirms
cleanup. Run commands and collect artifacts over SSH; workload queues belong to
the client or execution service.

## Build

Use the Go version in `go.mod`. Local macOS VMs require Apple Silicon and Tart;
image builds also require Packer. The console needs no frontend build.

```sh
make check
make build
./bin/virfield -help
```

On macOS, set `SIGN_IDENTITY` to your code-signing identity when building binaries
that need persistent Full Disk Access. Without it, builds keep ad-hoc signatures.
Store configuration, credentials, VM disks and installed binaries outside Git.

## Install and use

Follow [Operations](docs/OPERATIONS.md) to initialize a deployment, configure
storage and start the service. Then build a golden image, acquire a lease, wait
for readiness and generate pinned SSH configuration. Export results before
releasing the lease.

For an MCP client, start with `virfield_help`, then `virfield_status`. The embedded
help covers the same workflows as the repository documentation.

| Guide | Use it to |
| --- | --- |
| [Agent guide](docs/AGENT-GUIDE.md) | Call MCP tools, connect over SSH and handle errors |
| [Image pipeline](docs/IMAGE-PIPELINE.md) | Select versions, build images, transfer or publish them |
| [Operations](docs/OPERATIONS.md) | Install, configure, upgrade, diagnose and restore |
| [Environment checks](docs/ENVIRONMENT.md) | Check requirements and qualify a workload |
| [Remote debugging](docs/REMOTE-DEBUG.md) | Generate a SwiftPM or DuckDuckGo debugging workspace |
| [Development](docs/ARCHITECTURE.md) | Find modules, preserve lifecycle rules and build a release |
