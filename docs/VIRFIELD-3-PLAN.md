# Virfield 3.0 implementation plan

Virfield 3.0 is one control and access plane for physical hosts, macOS and
Linux VMs, interactive developers and automated workloads. It reuses the v2
durability and guest-policy code, delegates virtualization to established
runtimes, and keeps workload execution in Execution Broker.

## Current delivery status — 2026-10-07 (evening)

The agreed first implementation stage is **local Tart + Orchard adoption**;
the broader target architecture below remains aspirational. Tart adoption
follows [the Tart image adoption design](superpowers/specs/2026-10-07-tart-image-adoption-design.md):

| Step | State |
|---|---|
| Runtime adapters, host/storage observation, Hosts/storage console | Done (`8830ad9`, `ceb2747`, `1ec30c4`) |
| Guest identity from the template; per-lease SSH isolation for Tart ([plan](superpowers/plans/2026-10-07-tart-guest-identity.md)) | Done, unit-tested |
| Adoption pipeline: Tart image source, `internal/qualify`, Packer recipe, `internal/tartimage`, controller Tart phases ([plan](superpowers/plans/2026-10-07-tart-adoption-pipeline.md)) | Done, unit-tested, reviewed; recipe validated with Packer plugin 1.21.0 |
| Live service switched to Tart (separate `state_dir`; Lume config and binary kept for rollback) | Done; Lume lease released |
| Live golden builds and two-clone acceptance (Ventura 13.7.4 + Xcode 15.2, Monterey 12.7.6 + Xcode 13.4.1) | **Passed** 2026-10-07: both goldens qualified by the service; 23/23 two-clone checks each ([record](VERIFICATION.md#tart-image-adoption-2026-10-07)) |
| macOS 27 UI-test golden (`macos27`) and Broker v1 smoke on Tart | **Passed** 2026-10-08, Broker unchanged ([record](VERIFICATION.md#macos-27-ui-test-golden-and-broker-smoke-2026-10-08)) |
| VS Code remote-debug on Tart leases; GHCR publication | Not started |
| Remote nodes R1: bare-metal SSH provider (2014 MacBook Pro, Bazzite HTPC), per-host capacity, Wake-on-LAN | Design proposed, awaiting approval |
| Orchard live pool acceptance | Not started |

The local Tart golden, two-clone and Broker smoke parts of the Phase 1 exit
gate are met; Orchard is not. Live runs exposed six defects, all
fixed with tests (see the verification record). One `virfieldd` still
selects one runtime at a time; see the Obsidian Virfield handoff for live
deployment paths.

## Outcome

One client contract covers local and remote targets without hiding real
capability differences:

```text
VS Code / Web Console / CLI / MCP        Balda / CI
                 |                           |
                 v                           v
          Virfield Control <--------- Execution Broker
      leases, policy, images, audit          runs
                 |
                 v
          Virfield Access Gateway
       SSH / desktop / debug channels
                 |
      +----------+-----------+----------------+
      |                      |                |
      v                      v                v
bare-metal host       Orchard / Tart    node-agent / libvirt
macOS or Linux        macOS/Linux VM    Linux/Hackintosh VM
```

The common lifecycle is `discover -> acquire -> connect -> renew -> release`.
Build, test and agent workflows remain outside the VM lifecycle API.

## Product and service boundaries

| Component | Owns | Does not own |
|---|---|---|
| `virfield-control` | Hosts, capabilities, environment profiles, qualified artifacts, leases, connection grants, audit and provider reconciliation | Arbitrary commands, build/test interpretation, VM disk formats or debugger protocol |
| `virfield-node-agent` | Host inventory, local provider invocation, fixed health operations, endpoint discovery, access forwarding and optional Wake-on-LAN relay | Global scheduling, user authorization or workflow execution |
| `virfield-access` | Short-lived authorized byte channels for SSH, desktop and debug services | Guest credentials, VM lifecycle or commands transported inside SSH |
| `virfield-image-worker` | Reproducible adoption recipes, guest qualification, sanitization, evidence and artifact promotion | Lease scheduling or interactive access |
| `vscode-virfield` | Target selection, build/sync/debug UX and LLDB-DAP configuration through the Virfield CLI/SDK | VM scheduling, queueing, debugger implementation or Broker runs |

`virfield-console` is the single human-facing web application over Control and
Access. Execution runs displayed in it are read and controlled through the
Execution Broker API; Virfield does not copy the Broker run state machine.

Start as a modular Go application, not a collection of mandatory network
services. Split `node-agent` because it is host-local and privileged. Split the
image worker when remote workers are introduced. Access may run in the control
process until independent scaling or exposure requires a separate deployment.

## Targets, capabilities and providers

A target is either a reserved physical host or a runtime VM. Northbound clients
select capabilities, not a hostname or runtime-specific image:

```yaml
requirements:
  os: darwin
  arch: amd64
  xcode: "15.2"
  capabilities: [ssh, desktop, lldb-dap, ui-tests]
  trust_tier: lab
```

The resulting lease pins all implementation details:

```yaml
lease:
  id: lease-...
  target_id: target-...
  target_kind: vm
  provider: libvirt-qemu
  host_id: linux-i7
  environment_profile: ventura-xcode-15.2-ui-v1
  artifact_digest: sha256:...
  expires_at: ...
```

Provider matrix:

| Provider | Host and target | Purpose |
|---|---|---|
| `bare-metal` | Intel/Apple Silicon macOS or Linux host | Direct debugging, UI tests, builds and hardware-bound work with exclusive or declared shared capacity |
| `tart-local` | Apple Silicon Mac -> macOS/Linux VM | One-host development and first migration target |
| `orchard` | Apple Silicon Mac fleet -> Tart VM; Linux fleet -> Vetu VM | Production multi-host lifecycle, scheduling and native forwarding |
| `libvirt-qemu` | Linux x86_64 -> Linux or OpenCore/macOS VM | Personal lab and Hackintosh pool; qcow2 overlay, libvirt lifecycle and hypervisor VNC |
| `anka` / `parallels` | Intel or Apple Silicon Mac -> supported VM | Optional commercial Intel/macOS coverage; selected only when installed and licensed |

`lume-legacy` remains an adapter for observing, leasing and retiring current
images during migration. No target is ever managed by two providers.

Capabilities remain explicit. A Linux target may expose `lldb-server` or
`gdbserver`; macOS exposes `debugserver`; a bare-metal lease cannot claim
disposable rollback; a Hackintosh target is always `trust_tier: lab`.

## Transparent access

Clients request a connection grant instead of receiving a routable target IP:

```http
POST /api/v3/leases/{lease_id}/connections
{"service":"ssh","ttl_seconds":300}
```

The response identifies a short-lived local or WebSocket endpoint and the
pinned remote identity. `virfield-access` selects a connector:

| Connector | Data path |
|---|---|
| `private-direct` | Gateway to an allowlisted target address on a private/Tailscale network |
| `orchard-forward` | Orchard authenticated SSH or port forwarding to its owning worker |
| `node-forward` | Gateway to node-agent, then to a bare-metal host or libvirt guest private endpoint |
| `local-loopback` | Existing v2 loopback tunnel for one-host migration |

The first remote deployment should use Tailscale or an existing WireGuard
network between Control, Access and nodes. This avoids implementing NAT
traversal and a proprietary overlay. A reverse mTLS/QUIC node tunnel is a later
connector only if an overlay cannot be installed.

Supported logical services are:

| Service | Target endpoint |
|---|---|
| `ssh` | Guest or host SSH with host-key pinning and lease-specific caller key |
| `desktop` | macOS Screen Sharing, runtime VNC or libvirt VNC, exposed to Web through noVNC/WebSocket |
| `debug` | Loopback `debugserver`, `lldb-server` or `gdbserver` port through the protected path |
| `agent` | Versioned Virfield guest-agent health and bootstrap channel where supported |

Access forwards bytes and enforces lease/grant lifetime. It does not terminate
SSH, accept arbitrary destinations or expose a raw provider network.

## Linux/Hackintosh lab provider

The Linux node-agent uses libvirt rather than teaching Virfield QEMU process
management:

1. Resolve a qualified profile to pinned qcow2, OpenCore EFI/NVRAM and metadata
   artifacts.
2. Create a per-lease qcow2 overlay, unique domain UUID, MAC address and NVRAM.
3. Attach a one-time read-only bootstrap medium for the expected guest identity
   and management key, then start the libvirt domain.
4. Discover the private endpoint through the configured DHCP/libvirt network and
   require SSH plus OS/Xcode/profile attestation before readiness.
5. Stop and undefine the domain, remove only its overlay/bootstrap data and
   confirm absence before releasing capacity.

Normal desktop access uses guest Screen Sharing when qualified. Hypervisor VNC
is the installation/recovery fallback and remains behind Access. The image
worker may build this format, but lab artifacts never satisfy a production
profile and are never substituted for an Apple-hardware target.

## Physical hosts and Wake-on-LAN

Every physical host has a durable record independent of its provider:

```yaml
host:
  id: intel-macbook
  os: darwin
  arch: amd64
  labels: [xcode-15, ui]
  power:
    mode: wake-on-lan
    mac: "00:11:22:33:44:55"
    relay_host_id: always-on-linux
    ready_timeout_seconds: 180
    idle_action: none
```

Wake-on-LAN is optional and applies to physical hosts, never directly to a VM.
The configured relay must be an online node-agent on the same broadcast domain.
Control persists a host reservation and `waking` intent before the relay sends
an allowlisted magic packet. The host becomes schedulable only after its agent
reconnects and fresh inventory passes. Repeated packets are safe; an expired
wake deadline releases the reservation with a visible error.

The default idle action is `none`. Optional sleep or shutdown is a separate
policy executed only by the target's node-agent after the last lease is gone,
the idle grace period passes and no image or maintenance job owns the host.
Wake support, wired/Wi-Fi behavior, power-adapter requirements and measured wake
time are host capabilities established by an acceptance test, not assumptions.

## Hosts, storage containers and durable availability

Physical hosts, attached storage and runnable artifacts are separate durable
objects:

```text
Host
  +-- StorageContainer: local
  |     +-- ImageArtifact / VM
  +-- StorageContainer: samsung
        +-- ImageArtifact / VM
```

An inventory miss never deletes configuration. Every object has `availability`,
`reason`, `last_seen_at` and the identity of the observation that established
that state. Examples are `host_offline`, `host_sleeping`, `volume_unmounted`,
`volume_identity_mismatch`, `provider_unreachable` and `artifact_missing`.
Deletion is a separate explicit operation and requires observing the owning host
and container online before absence can be confirmed.

Storage containers use a stable operator ID and a probed device identity, not a
mount path alone. On macOS the node agent records the volume UUID and mount
point; on Linux it records the filesystem UUID/device identity and mount point.
A mount appearing at the expected path with the wrong identity fails closed.

Tart uses one configured `TART_HOME` per storage container. The local Mac disk,
the current Samsung SSD and future external SSDs are therefore distinct groups
with independent free-space, cache and availability state. Orchard-owned remote
storage is grouped under its worker/host identity. Libvirt pools are exposed as
containers in the same model.

The Console hierarchy is `host -> storage container -> images/VMs`. An offline
Mac or Linux host remains visible with its last inventory and a Wake button only
when its Wake-on-LAN policy and a live same-L2 relay are available. An unavailable
SSD leaves its image profiles visible but unschedulable with the exact reason.

Capacity is also per physical host. Each host reports total, allocatable,
allocated and available CPU/RAM plus its VM-slot limit. Local Tart subtracts
`memory_reserve_bytes` (4 GiB by default) before Virfield admits leases. Orchard
workers must advertise allocatable resources after the same reserve, so the
Orchard scheduler selects another matching worker when one host is full.

For an Orchard host with 16 GiB RAM and a 4 GiB user reserve, run the worker
with `org.cirruslabs.memory-mib=12288` and additionally publish
`ai.virfield.physical-memory-mib=16384`. Virfield fails closed with
`capacity_policy_mismatch` if the controller config and worker advertisement
would let Orchard consume the reserve. Linux/Vetu workers also advertise
`ai.virfield.vm-slots`; Tart uses Orchard's standard
`org.cirruslabs.tart-vms` resource.

Local Tart configuration keeps disk identity and capacity policy together:

```json
{
  "runtime": {"provider": "tart", "tart": {
    "binary": "/opt/homebrew/bin/tart",
    "host_id": "macbook-local",
    "max_vms": 2,
    "memory_reserve_bytes": 4294967296,
    "disk_reserve_bytes": 21474836480,
    "containers": [
      {"id": "local", "kind": "local", "path": "/Users/admin/.tart"},
      {"id": "samsung", "kind": "external", "path": "/Volumes/Samsung/tart", "volume_uuid": "..."}
    ]
  }}
}
```

Orchard uses one policy per worker. The worker advertises the same allocatable
budget; Orchard then selects the specific host:

```json
{
  "runtime": {"provider": "orchard", "orchard": {
    "url": "https://orchard.example",
    "service_account": "virfield",
    "token_file": "/absolute/orchard.token",
    "pool_id": "personal",
    "worker_labels": {"pool": "personal"},
    "host_limits": {
      "old-macbook": {
        "max_vms": 2,
        "memory_reserve_bytes": 4294967296,
        "wake_on_lan": {"mac_address": "00:11:22:33:44:55", "broadcast_address": "192.168.1.255:9"}
      },
      "linux-i7": {"max_vms": 3, "memory_reserve_bytes": 4294967296}
    }
  }}
}
```

## VS Code and Execution Broker

Interactive debugging does not pass through Execution Broker:

```text
vscode-virfield -> Control lease -> Access SSH/debug channels -> target
```

The extension calls a stable Go CLI/SDK for acquire, renew, sync, tunnel and
release. It reuses VS Code Debug Adapter Protocol and an installed LLDB-DAP or
CodeLLDB adapter. The existing v2 build, rsync, `debugserver`, DeviceSupport and
LLDB command generation becomes the initial CLI implementation instead of
being discarded.

Automated work uses a separate path:

```text
Balda/CI -> Execution Broker run -> Control lease -> Access SSH -> Runner
```

Broker continues to own queues, retries, Human approval, credentials,
repository revisions, commands, `.xcresult`, artifacts and publication. It
receives a target-independent lease and connection grant, never Orchard,
libvirt or Tart credentials.

## Image and environment model

An environment profile describes the result; provider artifacts implement it:

```yaml
environment_profile:
  id: ventura-13.6-xcode-15.2-ui-v1
  expected:
    os: darwin
    arch: [arm64, amd64]
    macos: "13.6"
    xcode: "15.2"
  policy:
    ssh: isolated-per-lease
    sip: disabled
    tcc: ui-test
    desktop: required
  artifacts:
    tart: ghcr.io/example/ventura-xcode@sha256:...
    libvirt-qcow2: oci://example/ventura-xcode-qcow2@sha256:...
    anka: registry/template:tag
```

Each artifact has its own provenance and runtime acceptance. Shared guest probes
verify exact OS/Xcode, SSH, debug policy, UI automation and sanitization. Runtime
probes additionally verify clone, boot, access, stop and deletion. Promotion
requires disposable-clone acceptance for that provider; evidence from Tart does
not automatically promote the qcow2 or Anka artifact.

Cirrus/Packer supplies upstream Tart images. Tart owns its disk layout and OCI
transport. Libvirt owns domain lifecycle; qcow2/OpenCore packaging is a separate
lab recipe. Anka/Parallels own their native layouts. Virfield stores references,
digests, qualification evidence and promotion state rather than translating VM
directories between formats.

## Code reuse plan

| Existing code | Virfield 3.0 treatment |
|---|---|
| `internal/store` | Keep SQLite, migrations, backups, events and idempotency; add durable hosts, storage containers, targets, providers, grants and profile artifacts through additive migrations |
| `internal/control` | Keep persist-before-effect phases, reconciliation, deadlines and quarantine; generalize the existing `Backend` seam into provider selection and per-host capacity |
| `internal/domain` | Add `Host`, `Target`, `CapabilitySet`, `ProviderRef`, `EnvironmentProfile` and `ConnectionGrant`; retain `/api/v1` lease fields as a compatibility projection |
| `internal/guestssh` | Keep re-keying, strict sshd policy, readiness and host-key pinning; parameterize guest user/home and transport connector |
| `internal/images` | Keep Xcode, SIP/TCC/AMFI, Peekaboo, desktop, DeviceSupport and evidence probes; move Lume creation, Recovery and Setup Assistant behind legacy/runtime-specific workers |
| `internal/registry` and `internal/catalog` | Keep allowlists, immutable resolution and version compatibility; add Tart and generic OCI-artifact media types without parsing runtime disk layouts |
| `internal/httpapi`, `internal/client`, `internal/mcpadapter` | Keep auth, limits and API-only clients; introduce `/api/v3` additively and implement v1 with the same controller during migration |
| `internal/control/tunnel.go` | Reuse revocation and bounded forwarding behavior as the first `local-loopback` connector; move destination selection behind the connector interface |
| `internal/client/vscode.go` | Preserve tested LLDB commands and sync helpers in the CLI/SDK; make the VS Code extension a thin caller and retire generated workspaces only after parity |
| `internal/lume` and deploy patches | Wrap as `lume-legacy`; delete only after every retained image and active lease has a replacement and rollback window has closed |

External dependency policy:

| Need | Reuse |
|---|---|
| Single-host Apple Silicon virtualization and image transport | Tart |
| Apple Silicon fleet lifecycle and native forwarding | Orchard |
| Upstream macOS images and reproducible image construction | Cirrus templates and Packer |
| Linux VMs | Vetu through Orchard, or libvirt when already present |
| Linux/Hackintosh lab lifecycle | QEMU/KVM, libvirt and OpenCore |
| Intel macOS fleet when commercial software is acceptable | Anka Build Cloud; Parallels for a standalone Monterey host |
| Private multi-site reachability | Existing Tailscale/WireGuard network |
| Browser desktop | noVNC as the frontend over Virfield-authorized WebSocket forwarding |
| Interactive debugger | VS Code DAP plus LLDB-DAP/CodeLLDB |

Do not add a generic remote-shell API, custom hypervisor, custom debugger,
second workflow engine, second artifact store or custom overlay network.

## Delivery plan

### Phase 1 — Tart and Orchard adoption (in progress)

| Work | Reuse-first implementation |
|---|---|
| Keep the existing durable controller | Reuse SQLite, lease/job journal and lifecycle reconciliation; preserve v1 API and Broker compatibility |
| Add runtime adapters | Tart local VM lifecycle and Orchard remote worker/VM lifecycle implement the shared provider seam |
| Show host and storage state | Report configured hosts, containers, availability reasons, disk/RAM/CPU and VM-slot capacity in the web console |
| Adopt a pinned upstream image | Pull a digest-pinned Cirrus Tart image, verify OS/Xcode, apply Virfield guest policy and qualify a local derived golden; GHCR publication follows acceptance |
| Accept each runtime | Prove clone, boot, unique SSH identity, access, release and confirmed cleanup against real Tart; separately exercise Orchard pool and restart behavior |
| Preserve legacy backend | Keep Lume as an explicit fallback during migration; do not run one VM through two runtimes |

Exit gate: a pinned Cirrus-derived golden passes two disposable Tart clones;
Orchard lifecycle has a separate passing acceptance run; current Lume workflows
and Broker v1 smoke remain unchanged. Mixed provider routing is not part of this
exit gate and must not be implied by a one-runtime-per-daemon configuration.

### Phase 2 — node-agent, physical hosts and access

| Work | Reuse-first implementation |
|---|---|
| Add `virfield-node-agent` | Reuse host resource probes and fixed-operation plan rules; publish signed inventory and heartbeats |
| Add bare-metal provider | Reserve an existing host without cloning; expose honest cleanup and exclusivity capabilities |
| Add Access grants/connectors | Start with current loopback tunnel, private/Tailscale direct routing and node forwarding |
| Add desktop gateway | Use Screen Sharing/runtime VNC plus noVNC; keep target ports private |
| Add Wake-on-LAN | Allowlisted host configuration, same-L2 relay, durable wake state, readiness deadline and acceptance test |

Exit gate: a sleeping Intel Mac is woken, reserved, reached by pinned SSH and
desktop, used for a debug smoke, released and never assigned concurrently.

### Phase 3 — additional VM providers and lab targets

| Order | Provider slice and gate |
|---|---|
| 1 | `tart-local`: adopt a pinned Cirrus Ventura base, install exact Xcode 15.2, run existing guest qualification and pass two disposable clones |
| 2 | `orchard`: map the same target contract to its controller, scheduling and forwarding; prove worker/control restart and drain |
| 3 | `libvirt-qemu`: qualify one Linux guest and one personal Hackintosh qcow2/OpenCore profile; prove overlay isolation, VNC/SSH and deletion |
| 4 | `bare-metal` Intel/macOS profile: exact Xcode/UI/debug qualification without claiming disposable rollback |
| 5 | Optional Anka/Parallels and Vetu adapters only for hosts actually present in the deployment |

Exit gate: the same acquire/connect/release acceptance suite passes for each
enabled provider, with provider-specific capability and trust assertions.

### Phase 4 — user products and Broker integration

| Work | Reuse-first implementation |
|---|---|
| Build `vscode-virfield` | Thin TypeScript extension over the Go CLI/SDK; reuse existing build/rsync/LLDB helpers and standard debug adapters |
| Upgrade Web Console | One view for hosts, power, capacity, profiles, artifacts, leases, connections, image jobs and linked Broker runs |
| Upgrade Broker client | Preserve its run model; replace direct VM assumptions with v3 capability request and SSH connection grant |
| Keep MCP narrow | Host/profile discovery, lease lifecycle, grants and diagnostics; run/workflow tools stay in Broker MCP |

Exit gate: VS Code debug, Web VNC/terminal, Balda run, unit test and UI test all
target local or remote backends without client-side provider conditionals.

### Phase 5 — migration, hardening and retirement

| Work | Required proof |
|---|---|
| Production safety | Cross-lease credential rejection, expired grant revocation, target drift quarantine and no public target ports |
| Durability | Restart Control, Access, node-agent and provider during every lifecycle phase without duplicate effects or early capacity release |
| Fleet operations | Drain, Wake-on-LAN failure, host loss, full disk, cache miss, network partition and mixed-architecture selection |
| Supply chain and recovery | Pinned runtime/image digests, signed evidence, backup/restore, profile revocation, upgrade and rollback rehearsal |
| Legacy retirement | Stop new Lume images, migrate retained profiles, wait the rollback window, then remove Lume patches and Setup Assistant OCR code |

Exit gate: every durable object has one source of truth—Broker Run, Virfield
Host/Lease/Profile/Grant, provider target, and immutable artifact digest—and
operators can trace between those IDs from the Console.

## First implementation slice

The first accepted vertical slice is:

```text
existing virfieldd and durable lease controller
  + local Tart provider
  + one digest-pinned Cirrus macOS image
  + Virfield image adoption and guest-policy qualification
  + two disposable Tart clone / boot / SSH / release checks
  + unchanged Lume and Broker v1 compatibility
```

Orchard is a parallel adapter slice and requires its own live pool acceptance;
unit tests do not prove Orchard deployment readiness. After Tart adoption, add
node-agent and bare-metal host access, then mixed-provider selection only when
the controller can safely route a lease to the selected host/runtime.
