# Compositional macOS execution platform

This document inventories the product as it exists on 2026-10-07 and defines a
replacement architecture without assuming that the current Virfield boundaries
are correct. It covers Virfield, Execution Broker/Balda, interactive developers,
image production, test execution and a multi-host fleet.

## Decision

Virfield should become a policy and product adapter over established runtimes,
not another VM runtime or general workflow engine.

| Keep in Virfield | Delegate | Extract |
|---|---|---|
| Product-facing lease/profile API, guest qualification, per-lease identity, policy attestation and compatibility with Execution Broker | Tart for local VM lifecycle and OCI transport; Orchard for fleet scheduling and routing; Cirrus/Packer for upstream image construction; Vetu or containers for Linux | VS Code integration, interactive console/VNC gateway and image adoption worker can ship separately while sharing one versioned API |
| Versioned image policy and promotion from upstream digest to a verified Virfield digest | Execution Broker for queues, runs, agents, Human approval, test commands and artifacts | Runtime providers behind capability-based adapters: Orchard/Tart first, Lume migration-only, Anka or Orka only if commercially selected |

The first production target is:

```text
Balda / Web UI / VS Code
          |
          v
Execution Broker (runs) ---- Virfield Control (leases, policy, image qualification)
                                      |
                                      v
                         Orchard Controller (fleet lifecycle)
                            /                    \
                           v                      v
                 Tart workers on Macs      Vetu workers on Linux
                           |
                           v
          pinned Cirrus base -> Virfield-derived golden -> disposable clone
```

Lume remains readable and releasable during migration, but no VM may be owned by
both Lume and Tart/Orchard. A runtime and image format are part of the durable VM
identity.

## Product use cases

### Consumers and entry points

| Use case | Required outcome | Product owner |
|---|---|---|
| Balda coding task | Start a durable run from an allowed repository revision, show progress, pause for Human approval and return verified artifacts | Execution Broker |
| Agent-requested macOS environment | Select an exact qualified image, acquire a disposable VM, obtain pinned SSH access and release it idempotently | Virfield Control |
| Interactive developer session | Choose an image, open terminal or desktop, renew a lease and destroy the VM without knowing the runtime host | Virfield Web/CLI |
| VS Code remote debugging | Build locally or remotely, synchronize the selected product, launch it under `debugserver`, attach LLDB-DAP and keep system symbols matched to the guest | Virfield VS Code extension |
| Operator administration | See hosts, capacity, images, jobs, leases, policy failures, cache pressure and audit history; quarantine or repair explicit resources | Virfield Control/Web |
| Automation client | Use authenticated HTTP/MCP with stable schemas, scoped principals, idempotency keys, events and machine-readable errors | Virfield Control |
| Direct VM desktop | Open a time-bounded VNC/Screen Sharing session through the owning host without exposing the VM network publicly | Virfield Console gateway |
| Headless worker | Boot from a prepared image with SSH/guest agent ready; no Setup Assistant or manual login in the lease path | Runtime plus qualified image |
| Image engineer | Import an upstream image by immutable digest, apply a versioned policy, verify it, publish a derived image and retain evidence | Virfield Image worker |
| CI system | Request macOS or Linux capacity by capabilities rather than by host name or implementation-specific image layout | Execution Broker plus Virfield/Orchard |

### Workload execution

| Use case | Acceptance condition | Boundary |
|---|---|---|
| Swift unit tests | Exact source revision, Xcode/toolchain and command are recorded; `.xcresult`, logs and exit status are exported before cleanup | Broker/Runner executes; Virfield supplies the VM |
| macOS application tests | App launches in the guest and test outputs survive VM deletion | Broker/Runner |
| XCUITest and UI automation | Logged-in WindowServer, stable resolution, Accessibility, Screen Recording and AppleEvents policy are verified before scheduling | Image qualification plus Broker/Runner |
| Screenshot-driven UI control | Peekaboo or another approved tool proves required permissions locally in the guest; screenshots and actions are bounded to the lease | Guest profile/Runner |
| Build-only CI | Compile, sign and package with an exact Xcode; no desktop is required | Broker/Runner |
| Interactive debugging | The selected executable and symbols match; `debugserver` listens only through a protected tunnel | VS Code extension plus guest profile |
| Long-running investigation | Lease renewal is explicit and bounded; restart/suspend behavior is declared by profile | Virfield Control/runtime |
| Agent workflow | Provider credentials are leased to one run, checkpointed before destruction and never baked into an image | Execution Broker credential service |
| Artifact publication | Checksums, source/head revisions and quality evidence are verified before applying output to a managed repository | Execution Broker |
| Cancellation and timeout | Intent is durable, the workload stops, artifacts are finalized when possible and resource cleanup is independently confirmed | Execution Broker and Virfield |

Virfield must not grow a generic command-execution API. Unit tests, UI tests and
builds are product use cases, but their commands, retries, artifacts and Human
gates belong to Execution Broker/Runner. Virfield qualifies and leases the
environment.

### VM lifecycle and access

| Use case | Acceptance condition |
|---|---|
| Discover capacity | Fresh host/runtime inventory, CPU/RAM/disk budgets and capability labels are visible; stale inventory fails closed |
| Acquire | One request maps to one durable lease despite lost responses; a compatible host and immutable image digest are pinned |
| Clone | Clone uses the selected runtime and storage class; source image remains immutable |
| Boot and readiness | Runtime reports running, guest agent/SSH responds, expected identity is attested and policy probes pass |
| Credential isolation | Each clone gets a unique administrator password, SSH host key, management key and caller key; inherited access is rejected |
| Direct SSH | Caller receives connection metadata and a host-key pin, never a shared private key or bootstrap password |
| Routed SSH | A loopback/jump-host endpoint preserves the guest host-key identity and closes with the lease |
| Desktop access | VNC/Screen Sharing is proxied with per-lease authorization, short lifetime and audit events |
| Renew | A bounded absolute deadline extends an active lease without changing its identity |
| Release/expiry | Stop and deletion are separate observed effects; capacity is freed only after confirmed absence |
| Crash recovery | Controller restart reconciles durable intent with runtime inventory and never blindly replays uncertain mutations |
| Quarantine | Drift, unknown mutation outcome, failed re-keying or failed policy attestation keeps capacity reserved for operator action |

### Images, Xcode and guest policy

| Use case | Acceptance condition |
|---|---|
| Select macOS/Xcode | Exact guest version/build, Xcode version/build and CPU architecture are declared as a profile |
| Pin upstream | OCI tag is resolved to a digest before download; digest is retained as provenance |
| Prepare base OS | A supported upstream image boots with a known administrator, autologin where required, SSH and no pending Setup Assistant |
| Install Xcode | Official archive or approved upstream layer is signature-checked; exact version is verified without changing the host Xcode |
| Complete Xcode setup | License, first launch, components, `xcode-select` and tool execution pass after reboot |
| Apply debug policy | SIP, Gatekeeper, AMFI, Developer Tools and TCC settings are explicit profile fields, not hidden consequences of choosing Xcode |
| Qualify UI automation | Accessibility, Screen Capture, Full Disk Access, AppleEvents/System Events and Peekaboo probes pass after reboot |
| Prepare LLDB symbols | Matching macOS DeviceSupport/dyld symbols are available on the host that will run LLDB |
| Install guest agent | IP discovery, controlled exec/health and disk resize behavior use a version-pinned guest agent where supported |
| Sanitize transport image | Workload data, private keys, tokens, logs and machine-specific credentials are absent before publication |
| Publish derived golden | Result is pushed under a unique tag, recorded by digest and pull-back tested |
| Promote | Two disposable clones pass boot, isolation, workload, artifact export and cleanup before the image becomes schedulable |
| Revoke | A digest can be disabled immediately without deleting registry history |
| Retain provenance | Upstream digest, recipe digest, runtime version, policy, test evidence and derived digest form one signed record |

### Fleet and operations

| Use case | Acceptance condition |
|---|---|
| Multi-host scheduling | Hosts advertise OS, architecture, runtime, labels, resources and image/cache availability |
| Mixed hardware | Apple Silicon macOS, Intel macOS and Linux are separate capability pools; no request silently changes architecture or OS |
| Host drain | New work stops, current leases finish or are explicitly migrated/canceled, and maintenance is observable |
| Storage pressure | OCI/IPSW caches have quotas and eviction; goldens and active overlays cannot be evicted |
| Network isolation | Guest egress, host access, exposed ports and shared directories are profile policy |
| Service identity | Controller, worker, Broker, operator and human principals have separate credentials and scopes |
| Availability | Controller state is backed up; workers can reconnect; runtime loss does not fabricate successful cleanup |
| Observability | Structured metrics, bounded logs, audit events and correlation IDs connect run -> lease -> VM -> host -> image digest |
| Upgrade | Controller, adapter and runtime versions have compatibility ranges and a drain/rollback procedure |
| Cost/capacity | Queue time, boot time, active time, cache hit rate, host utilization and failed qualification are measurable |

## Current Virfield implementation inventory

Status means code exists in this repository; it does not widen the live
acceptance claims in [Verification](VERIFICATION.md).

### Control plane

| Capability | Status | Current implementation |
|---|---|---|
| Authenticated API | Implemented | Bearer-authenticated `/api/v1`, owner/operator principals, scoped Broker token, request size limits, same-origin browser policy |
| MCP | Implemented | 18 tools: help/status, leases, renewal/release, jobs/events, SSH tunnels, catalog/create/build, Apple auth and GHCR resolve/pull/publish |
| CLI | Implemented | API-only operator/client CLI, SSH identity/config generation and VS Code workspace generation |
| Web UI | Partial | Local token login, status/capacity, acquire, build/delete image, jobs/events, lease release and SSH tunnel; no VNC console, catalog-driven image creation, multi-host view or full recovery UI |
| Durable state | Implemented | SQLite migrations, atomic lease/job/event/idempotency records, migration snapshots, backups and history retention |
| Lifecycle safety | Implemented | Persist-before-effect phases, bounded deadlines, no blind mutation replay, reconcile on restart, quarantine/needs-attention and explicit operator resolution |
| Capacity | Implemented for one host | Maximum two macOS VMs, external VM accounting, CPU/RAM reserve, disk/storage admission, clone location routing and fail-closed stale inventory |
| Multi-host fleet | Missing | One daemon owns one Lume endpoint and one local state database |

### Runtime, lease and storage

| Capability | Status | Current implementation |
|---|---|---|
| VM inventory/start/stop/delete | Implemented | Lume HTTP adapter plus fixed CLI operations for image-only paths |
| Disposable cloning | Implemented | APFS clone, cross-storage copy routing, source/destination location tracking and cleanup confirmation |
| Legacy encrypted image | Implemented with constraints | Identity-preserving `legacy_uuid`, one worker, no portable publication |
| Lease TTL | Implemented | 60 seconds to 24 hours, absolute renewal, expiry cleanup and capacity retained until deletion is observed |
| SSH isolation | Implemented | Password rotation, unique daemon key and host key, caller key, strict sshd policy, rejection of inherited image/cross-lease keys |
| SSH routing | Implemented | Direct guest IP or ephemeral loopback tunnel with the same host-key pin |
| VNC automation | Partial | Private loopback VNC drives Setup Assistant and Recovery; no user desktop gateway or lease VNC API |
| Space management | Partial | Resource admission, configured storage paths, disk checks, bounded logs/history; no distributed cache policy or fleet-wide storage accounting |

### Image factory and guest qualification

| Capability | Status | Current implementation |
|---|---|---|
| Catalog | Implemented | Apple restore metadata plus Xcode release/minimum-OS catalog and local available/downloaded/installed state |
| IPSW install | Implemented | Pinned HTTPS download, resume bounds, integrity/size checks and Lume create/setup stages |
| Setup Assistant | Implemented but expensive | Version-specific VNC/OCR state machine, FileVault choice, autologin and desktop readiness |
| Xcode acquisition | Implemented | Local signed app reuse, private archive input or Apple Developer session through a native WebKit helper |
| Xcode install/verify | Implemented | XIP/app transfer, signature and version checks, first-launch completion and reboot verification |
| Security profiles | Implemented | `default`, `sip-disabled`, `automation`; Recovery SIP flow plus Gatekeeper, AMFI and exact TCC database handling |
| UI-test profile | Implemented for accepted versions | Homebrew/tool recipe, `jq`, `socat`, `peekaboo`, `screenresolution`, `xcbeautify`, passwordless sudo and permission probes |
| Remote-debug preparation | Implemented | Developer Tools, `_developer`, NFS export of immutable dyld cache and host DeviceSupport extraction |
| Disk policy | Implemented | FileVault/encryption/lock evidence, portable image rejection and explicit legacy exception |
| GHCR | Implemented only for Lume format | Approved namespaces, digest/descriptor/space checks, pull, fresh sanitized rebuild, push and digest verification |
| Golden promotion | Implemented | Exact OS/Xcode/policy verification, clean shutdown, stopped template and disposable-clone acceptance runbook |
| Runtime-neutral images | Missing | Lume and Tart manifests/layouts are incompatible; current registry code rejects Tart images |

### Development and test workflow

| Capability | Status | Current implementation |
|---|---|---|
| VS Code config | Implemented but coupled | Generates lease-set-specific SwiftPM/DuckDuckGo workspaces, host tasks and helper scripts |
| LLDB remote attach | Implemented | Host LLDB-DAP -> SSH tunnel -> guest loopback `debugserver`, with matching DeviceSupport symbols |
| Host build/test | Implemented in generated tasks | Swift build/test runs on the host; selected products are incrementally synchronized to the guest |
| Guest unit/UI execution | Supplied, not owned | Qualified image and SSH exist; Execution Broker/Runner owns commands, results, `.xcresult` and cleanup |
| Balda integration | Supplied, not owned | Broker calls the REST lease/image contract; Virfield has no Balda session, queue, Human approval or artifact model |

## Execution Broker and Balda use cases

Execution Broker is already the correct owner for execution semantics. Its
current contract provides:

| Capability group | Implemented behavior |
|---|---|
| Admission and routing | Profiles, exact workflow digest, repository allowlist/immutable revision, runtime class, image selection, FIFO capacity and idempotent start |
| Durable run lifecycle | `accepted -> provisioning -> booting -> running <-> waiting_human -> terminal`, independent cleanup, deadlines, cancellation, event replay and acknowledgements |
| Balda/Human loop | Session-bound questions, approvals, requirement amendments, durable notification outbox and Mattermost delivery acknowledgement |
| Execution | Disposable Docker or Virfield macOS worker, guest-local checkout, Callee workflow roles, provider-specific agent profiles and bounded retries |
| Repositories | Approved folder discovery/grant, managed local Git remotes, new workspace creation and immutable revision pinning |
| Artifacts and quality | Checksummed patches, untracked/output archives, `.xcresult`, commit bundle, diagnostics, quality gate and controlled publication |
| Credentials | Per-run OAuth/API credential lease, encrypted host vault, exclusive ownership, refresh checkpoint before deletion and revocation/recovery |
| Managed hosts | Separately signed fixed operation plans with preflight, apply, verify and bounded rollback |

Consequences for Virfield:

- do not add task queues, arbitrary SSH commands, repository checkout, provider
  credentials, test interpretation or artifact storage;
- preserve a small lease/image API that Broker can call with an `image_id` and a
  caller SSH key;
- expose capabilities and immutable image identity so Broker chooses a valid
  worker without learning runtime-specific details.

## What upstream systems cover

### Tart, Cirrus and Orchard

| Need | Upstream coverage | Virfield responsibility after migration |
|---|---|---|
| macOS VM runtime | Tart uses Apple `Virtualization.framework` and provides create/set/clone/run/stop/delete | Provider adapter, ownership labels and policy mapping only |
| Local copy-on-write/storage | Tart owns local VM layout, clones, cache and new stacked images on supported hosts | Storage-class selection and capacity policy, not disk-layout code |
| OCI transport | Tart pulls/pushes Tart images to OCI registries and supports registry credentials | Allowlist, digest pinning, provenance, promotion/revocation policy |
| Initial images | Cirrus publishes vanilla/base/Xcode/runner images built with Packer, SSH and automation-friendly settings | Select/fork a digest, add Virfield policy and qualify it |
| Setup Assistant | Cirrus/Packer has already automated it for published images; Tart on macOS 27 also exposes Apple's first-boot guest provisioning API | Avoid Setup Assistant in normal adoption; retain a narrow fallback only for unsupported image builds |
| Guest communication | Tart Guest Agent provides reliable IP resolution, controlled `tart exec`, clipboard and disk resize support | Pin/install it and decide which functions policy allows; SSH remains the tenant boundary |
| Recovery and display | Tart supports Recovery boot and built-in/Screen Sharing/experimental Virtualization.framework VNC modes | Automate required recovery policy; provide an authenticated user gateway and acceptance tests |
| Multi-host lifecycle | Orchard supplies authenticated controller/worker architecture, REST/CLI, scheduling, SSH jump routing and port forwarding | Product leases, image qualification, tenant ownership and Broker compatibility |
| Linux fleet | Orchard can schedule Vetu workers on Linux; Vetu runs Cloud Hypervisor Linux VMs with Tart-compatible OCI concepts | Advertise a separate `linux` pool; Broker may also keep its existing container gateway |

Primary references: [Tart](https://github.com/openai/tart),
[Tart quick start](https://github.com/openai/tart/blob/main/docs/quick-start.md),
[Tart FAQ](https://github.com/openai/tart/blob/main/docs/faq.md),
[Tart Guest Agent](https://github.com/openai/tart-guest-agent),
[Cirrus macOS templates](https://github.com/cirruslabs/macos-image-templates),
[Orchard](https://github.com/openai/orchard), and
[Vetu](https://github.com/openai/vetu).

Tart and Orchard currently use FSL-1.1-ALv2: internal use is explicitly
permitted, while a competing commercial service is restricted until each
version's two-year Apache-2.0 conversion. This is a product/legal gate, not a
technical footnote. Cirrus image-template source is MIT licensed. Image contents
also remain subject to Apple's and tool vendors' licenses.

### Exact Ventura/Xcode 15.2 result

The current public GHCR tag inventory for
`ghcr.io/cirruslabs/macos-ventura-xcode` contains Xcode `15` and `15.4`, but no
`15.2` tag. The package page identifies `15.4` as the latest published Ventura
Xcode image. Apple confirms that Xcode 15.2 requires macOS Ventura 13.5 or later.

Therefore the reproducible path is:

1. Pin a compatible `macos-ventura-base` or `vanilla` Tart image by digest.
2. Install the official Xcode 15.2 archive with a versioned Packer/Ansible or
   Virfield adoption recipe.
3. Verify `sw_vers`, build number, `xcodebuild -version`, signature, first-launch
   state and the required debug/UI policy after reboot.
4. Publish the derived image to the private registry by digest and run the full
   two-clone acceptance before promotion.

Using the mutable `latest` tag or substituting Xcode 15.4 is not equivalent.
The public package is [macos-ventura-xcode](https://github.com/cirruslabs/macos-image-templates/pkgs/container/macos-ventura-xcode);
the compatibility source is [Apple's Xcode system requirements](https://developer.apple.com/xcode/system-requirements/).

### Alternatives

| System | What it can replace | Why it is not the default |
|---|---|---|
| Lume/CUA | Single-host lifecycle, VNC, Recovery/SIP and its own OCI format | Already integrated, but maintaining patches, Setup Assistant automation and a second image format creates most current complexity; keep only for migration/legacy goldens |
| Anka Build Cloud | Runtime, Intel and Apple Silicon nodes, controller, registry, REST/UI, quotas and CI integrations | Mature and the strongest option for an Intel+ARM commercial fleet, but licensed/proprietary and uses its own controller/registry model |
| MacStadium Orka | Apple Silicon fleet, OCI images, CLI/API, web UI and VNC-oriented connections | Commercial platform; a valid buy-versus-build choice when managed/on-prem Mac infrastructure matters more than ownership of the control plane |
| Managed CI | Noninteractive build/test runners with maintained images | Does not satisfy direct user VM management, custom VNC, long interactive debugging or exact retired Xcode availability; Xcode Cloud has removed Xcode 15.2 from current support |
| QEMU/UTM/Quickemu on Linux | Experimental/emulated machines and non-macOS guests | Not a production macOS pool: slow/unsupported combinations and Apple licensing require Apple-branded hardware for macOS/SDK use |

References: [Anka overview](https://docs.veertu.com/anka/),
[Anka Build Cloud](https://docs.veertu.com/anka/anka-build-cloud/),
[Orka Virtualization](https://macstadium.com/orka-virtualization),
[Orka web UI](https://orkadocs.macstadium.com/v3.3.0/docs/ui-quick-start),
[Apple macOS license index](https://www.apple.com/legal/sla/), and
[Apple Developer Program agreement](https://developer.apple.com/support/terms/apple-developer-program-license-agreement/).

## Host strategy

| Host | Production role |
|---|---|
| Apple Silicon Mac | Primary macOS VM worker using Tart/Orchard; capability labels include chip generation, RAM, storage, host OS and supported guest range |
| Old Intel MacBook | Separate `darwin-amd64` bare-metal runner for compatible unit/build tests, or an Anka 2 node if its exact hardware/host OS is supported and licensed; never pretend it is a Tart worker |
| Linux x86_64/i7 | Control plane, registry, Broker, containers and Vetu Linux VMs; no production macOS guest |
| Linux arm64 | Same plus native arm64 Linux Vetu workloads |
| Cloud Mac | Additional Tart/Orchard worker when host lifecycle, networking and Apple license terms are satisfied |

Apple's Ventura license allows up to two additional macOS VM instances on each
Apple-branded Mac for development/testing under the stated terms. Apple SDKs and
macOS must not be run on non-Apple-branded computers under the Developer Program
agreement. Linux-hosted macOS emulation is therefore excluded from the
production design. QEMU remains useful for Linux guests and laboratory research,
not as schedulable macOS capacity.

## Target components

### 1. `virfield-control`

The only northbound product service. It owns users/service principals, image
profiles, qualification records, leases, audit events and compatibility APIs for
Broker/MCP/Web. It calls Orchard or a local provider; it does not know Tart disk
paths.

### 2. `virfield-image-worker`

A restricted worker for adoption jobs. Input is an upstream digest plus a
versioned recipe; output is evidence and a derived digest. It performs SSH and
Recovery policy, Xcode/UI tools, sanitization and clone acceptance. It can start
inside the control repository, but its privileges and deployment are separate.

### 3. `virfield-console`

Stateless web UI and authenticated SSH/VNC gateway. It obtains short-lived
connection grants from control and routes to the owning host. It never receives
golden/bootstrap credentials or registry write credentials.

### 4. `vscode-virfield`

A real VS Code extension instead of generated project-specific files. It lists
leases, selects a target, launches build/sync/debug tasks and obtains tunnel
metadata. Project adapters such as DuckDuckGo are declarative profiles, not code
inside the VM manager.

### 5. Runtime providers

`orchard` is the production provider. `tart-local` is the one-host development
provider. `lume-legacy` supports observe/release/export during migration and then
is removed. A future `anka` or `orka` provider is added only after a commercial
selection, not as speculative abstraction.

## Required API model

```yaml
image_profile:
  id: ventura-13.6-xcode-15.2-ui-v1
  os: darwin
  arch: arm64
  runtime_format: tart
  source: ghcr.io/our-org/virfield-ventura-xcode@sha256:DERIVED
  provenance:
    upstream: ghcr.io/cirruslabs/macos-ventura-base@sha256:UPSTREAM
    recipe: sha256:RECIPE
  expected:
    macos_version: "13.6"
    macos_build: "22G..."
    xcode_version: "15.2"
    xcode_build: "15C500b"
  policy:
    ssh: isolated-per-lease
    sip: disabled
    gatekeeper: disabled
    amfi: automation
    tcc: ui-test
    desktop: required
    vnc: allowed-through-gateway
  acceptance:
    evidence_digest: sha256:EVIDENCE
    state: promoted
```

Every lease pins the profile revision, derived image digest, provider, runtime VM
identity, host, owner and expiry. Provider-native names are implementation data,
not user-selected global identifiers.

## Migration plan and gates

### Phase 1 — prove Tart adoption on one Mac

1. Add a capability-based runtime interface and a `tart-local` adapter for
   observe, clone, start, stop, delete, IP and Recovery/VNC metadata.
2. Parameterize guest username/home; remove `/Users/lume` and `lume/lume` from
   domain contracts.
3. Pull one pinned Cirrus Ventura base, install Xcode 15.2, run the existing
   Virfield security/debug qualification and publish a derived Tart image.
4. Acquire two ordinary clones through the existing lease API and pass SSH
   isolation, unit workload, UI probe, LLDB attach, artifact export and cleanup.

Gate: Broker's current macOS smoke and the VS Code remote-debug acceptance pass
without Lume handling those VMs.

### Phase 2 — delete duplicate image machinery

1. Make Tart OCI the default image identity and remove Lume manifest parsing from
   the new profile path.
2. Replace IPSW/Setup Assistant as the default factory with pinned upstream
   images and reproducible Packer/adoption recipes.
3. Retain only the policy unique to this product: exact version attestation,
   SIP/TCC/AMFI, per-lease re-keying, DeviceSupport and promotion evidence.
4. Mark Lume profiles legacy/read-only; prevent new builds and new leases after
   all required goldens have Tart replacements.

Gate: every supported profile is reproducible from digest+recipe, and disaster
recovery requires no private pre-existing VM directory.

### Phase 3 — use Orchard for the fleet

1. Deploy Orchard controller on Linux or macOS and one worker per Apple Silicon
   Mac; keep its API private and authenticated.
2. Map Virfield lease requirements to Orchard VM labels/resources/endpoints and
   reconcile by immutable external ID.
3. Add host capability discovery, drain, cache/status metrics and failure-domain
   aware scheduling to the Virfield view without duplicating Orchard's scheduler.
4. Route SSH and VNC through authenticated gateways; no direct public VM ports.

Gate: controller and one worker may restart without losing a lease, duplicating
a VM or freeing capacity before confirmed deletion.

### Phase 4 — separate user products

1. Move generated VS Code behavior into `vscode-virfield` with a stable extension
   API and declarative project profiles.
2. Replace the local embedded page with the console/gateway for hosts, images,
   leases, jobs, terminal and desktop.
3. Keep MCP focused on image selection, lease lifecycle and diagnostics; Broker
   remains the only run/workflow API used by Balda.
4. Remove `cmd/virfield-lume`, Lume patches and the VNC/OCR Setup Assistant code
   after the migration retention window and final legacy image deletion.

Gate: the product has one source of truth for each object—Broker Run, Virfield
Lease/ImageProfile, Orchard VM and OCI digest—and every UI links those IDs.

## Production acceptance checklist

| Area | Required proof |
|---|---|
| Functional | Broker unit/UI workflows, interactive SSH/VNC, VS Code attach, exact Ventura/Xcode profile and artifact export pass |
| Safety | Cross-lease keys fail, inherited credentials fail, VNC grants expire, no secret is present in image layers/events and uncertain effects quarantine |
| Durability | Restart control, Orchard and one worker at each lifecycle phase; no duplicate clone and no early capacity release |
| Fleet | Drain, host loss, cache miss, full disk, network partition and mixed architecture selection fail predictably |
| Supply chain | Runtime versions, FSL review, upstream and derived OCI digests, recipe digest, Xcode signature and evidence bundle are recorded |
| Operations | Backup/restore, metrics/alerts, upgrade/rollback, image revocation and legacy Lume retirement are rehearsed |

## Immediate implementation slice

The smallest useful change is not a broad rewrite. Add `tart-local`, guest
profiles and Tart digest identity while preserving the current lease REST
contract used by Broker. Prove one exact Ventura 13.6/Xcode 15.2/UI/LLDB image.
Only after that proof should Orchard replace single-host scheduling and Lume
image code be deleted.
