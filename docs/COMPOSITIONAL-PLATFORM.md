# Compositional platform boundaries

Current inventory reconciled 2026-10-16. This document defines responsibility;
[Verification](VERIFICATION.md) owns release evidence and
[the 3.0 plan](VIRFIELD-3-PLAN.md) owns future delivery. The
[earlier architecture discussion](history/COMPOSITIONAL-PLATFORM-before-2026-10-14.md)
is preserved as historical design, not current implementation inventory.

## Current implementation

| Component | Implemented responsibility |
|---|---|
| Virfield controller | Durable images/leases/jobs/events/idempotency, admission, expiry, quarantine and observed cleanup |
| Tart | Local VM lifecycle, storage, copy-on-write clones and native OCI transport |
| Cirrus/Packer adoption | Approved pinned bases, optional exact Xcode/download, qualified stopped goldens and fresh sanitized Tart publication |
| SSH nodes | Physical Mac/Linux host lease; Podman/QEMU/OpenCore macOS lab VM leases and secured qcow2 goldens |
| Access | Pinned SSH metadata and loopback SSH/desktop tunnels tied to the lease |
| Console/CLI/MCP | Host/storage inventory, templates, leases, jobs/events, build/delete actions, Screen Sharing and API clients |
| Execution Broker/Runner | Queues, runs, commands, repository revisions, approvals, execution credentials and workload artifacts |
| Orchard | Candidate adapter, not accepted for production use |

A successful lease does not mean a workload succeeded. Workload results and
`.xcresult` belong to Broker/Runner. Virfield has no generic command-execution
API, artifact store or workflow engine. Physical hosts are shared-account lab
resources, not disposable/multi-tenant VM substitutes.

The operator's VM account decision is standard `admin/admin`; automatic random
account-password rotation was removed in `0f8bc20` and later. Full installed
native/reboot/workload cycles passed for both Monterey inventories, new macOS27
and HTPC Sonoma/Sequoia; exact client/production scope is in Verification.
SSH key isolation and native GUI/new SSH acceptance remain part of Virfield.
Recovery of a confirmed drifted lease without deletion, durable drift/tunnel
diagnostics, complete resource refusal arithmetic, verified local golden transfer
and the optional legacy iOS profile are implemented with installed live receipts
in the [maintained plan](VIRFIELD-3-PLAN.md#operator-feedback-backlog). Revalidation
and transfer are explicit journaled client operations; the profile provisions
and verifies tools. Broker/Runner still own actual application builds.

## Target components

The 3.0 target adds capability/profile/target selection, durable host/storage
registry and short-lived authorized connection grants. Node-agent publishes
signed inventory and fixed operations; Access normalizes private/Orchard/node
routes and browser desktop/debug channels. Image-worker produces sanitized,
provider-specific artifacts with provenance and promotion/revocation evidence.

A thin VS Code extension reuses the CLI/SDK and standard DAP/LLDB. Console links
Broker runs instead of copying their state machine. Orchard supplies fleet
scheduling; optional libvirt/Vetu/commercial providers require independent
capability, isolation and cleanup acceptance. Existing Tailscale/WireGuard can
supply private reachability; do not build a proprietary overlay or hypervisor.

## Acceptance boundaries

Keep OS, architecture, Xcode, provider and lab/production trust explicit.
Acceptance for one runtime does not promote another artifact. A pinned image
plus qualification is not full clone, debug or UI-test acceptance. Unavailable
hosts/storage stay visible with a reason; ambiguous effects reserve capacity.
Do not advertise v3 grants, browser noVNC, node-agent, distributed image workers,
drain, signed promotion or VS Code extension as current capabilities.
