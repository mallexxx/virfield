# Virfield

Virfield is a Go control plane for local Tart macOS VMs and SSH-controlled
physical macOS/Linux nodes. Linux nodes can also host disposable macOS lab VMs
through rootless Podman/QEMU/OpenCore. It owns images, leases, capacity, pinned
SSH, desktop tunnels, durable jobs and confirmed cleanup. Execution Broker owns
queues, commands, runs, approvals and workload artifacts.

The maintained checkout is this repository. Current release hardening is
published on `codex/retire-lume-release-hardening`; `main` has not been promoted
to that candidate. The earlier `virfield-v3` worktree/branch is obsolete. Virfield switched to Tart on
2026-10-08; the runtime/factory/wrapper/build/install Lume code is removed. The separate old LaunchDaemon was disabled and booted out on 2026-10-14, with rollback files preserved. Current release gates are tracked
in [Verification](docs/VERIFICATION.md); old Lume runbooks are archived, not
installation instructions for the current product.

| Document | Authority |
|---|---|
| [Operations](docs/OPERATIONS.md) | Installation, configuration, services, client registration and recovery |
| [Agent guide](docs/AGENT-GUIDE.md) | MCP tool arguments, pinned SSH, retries and workflows; embedded help |
| [Image pipeline](docs/IMAGE-PIPELINE.md) | Tart/node image contracts, qualification and recovery |
| [Verification](docs/VERIFICATION.md) | Actual acceptance evidence, current defects and release gates |
| [Architecture](docs/ARCHITECTURE.md) | Current modules and lifecycle invariants |
| [Remote debugging](docs/REMOTE-DEBUG.md) | Existing generated VS Code workflow and its acceptance limits |
| [3.0 plan](docs/VIRFIELD-3-PLAN.md) | Delivery state and remaining target architecture |
| [Platform boundaries](docs/COMPOSITIONAL-PLATFORM.md) | Current product responsibilities and future components |

## Build

Go's minimum patch and dependencies are pinned in `go.mod`. The console is
embedded HTML/CSS/JavaScript and needs no frontend build. On macOS the native
Apple sign-in browser is also built. Runtime state, credentials, VM disks,
archives and installed binaries belong outside the checkout.

```sh
make check
make build
./bin/virfield -help
```

Release checks also include the pinned Staticcheck, module integrity,
`govulncheck`, Gitleaks and a clean archive/extraction test. A green unit suite
does not establish live workload or disaster-recovery acceptance.

## Supported deployment and release scope

The accepted runtime paths are local Tart and the R1–R3 SSH/node slices. Orchard
has an adapter but is not accepted for deployment. The broader 3.0 API, grants,
node-agent, browser desktop and VS Code extension remain roadmap work.
[Published v2.0.0](https://github.com/mallexxx/virfield/releases/tag/v2.0.0)
is a historical Lume release, not an archive of the current Tart implementation.
Use the current source and Operations until a new release passes its gates.
The [Broker macos27 reacceptance record](docs/VERIFICATION.md#broker-macos27-reacceptance)
records the current cross-project boundary and the evidence required to close it.

The operator requires the standard VM account `admin/admin`; removing automatic
random password rotation is removed in standard-account candidates (`0f8bc20` and
later). Samsung Monterey has two complete installed cycles; the remaining
provider/client matrix is open. SSH key isolation and native GUI/SSH key-storage checks remain
required. The [maintained plan](docs/VIRFIELD-3-PLAN.md) records this first priority,
the open macOS 27/Sonoma failures and all five requested follow-ups. Historical
passes do not establish acceptance of the remaining standard-password matrix.
