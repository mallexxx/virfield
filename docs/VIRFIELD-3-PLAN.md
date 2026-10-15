# Virfield 3.0 implementation plan

Delivery state reconciled 2026-10-15. The maintained repository's hardening
candidate is on `codex/retire-lume-release-hardening`; `main` is not yet promoted. The live
runtime is Tart plus SSH nodes; Virfield switched away from Lume on 2026-10-08.
Source/factory/wrapper/build/install retirement is implemented; the separate old
LaunchDaemon was disabled and booted out on 2026-10-14. This roadmap does not claim
that all 3.0 components are implemented or required as independent processes.
[Verification](VERIFICATION.md) owns defects and actual release acceptance.
The [original detailed design](history/VIRFIELD-3-PLAN-before-2026-10-14.md)
is archived; its future contracts remain reference designs, not current APIs.

## Phase 1 — Tart and Orchard adoption

| Work | State |
|---|---|
| Tart lifecycle, storage inventory and Console | Implemented; local live acceptance |
| Guest account parameterization and per-lease SSH | Implemented; Tart two-clone acceptance |
| Cirrus/Packer adoption and qualification | Implemented for configured profiles; Ventura/Monterey/macOS 27 accepted |
| Broker v1 smoke | Passed 2026-10-08 on Tart |
| Tart dynamic factory and GHCR publication | Full fresh factory/private portable push/pull/clone workload/cleanup roundtrip passed 2026-10-15 on `2f23ae6`; seven baseline goldens preserved |
| VS Code debug | Generated workspaces implemented; Tart live acceptance pending |
| Orchard | Adapter only; isolated access and live pool acceptance incomplete |

The complete Phase 1 exit gate is not met. Tart and SSH nodes compose today;
local Tart and Orchard cannot share one daemon's primary runtime selection.

## Phase 2 — node-agent, physical hosts and access

R1 pinned-SSH host leases and wake-by-lease, R2 Podman macOS VMs, and R3
Screen Sharing for VM leases are implemented and have dated live acceptance.
The R1–R3 specs describe these completed slices, not the full Phase 2 target.

Host exclusivity, shared sleep holds and node cleanup are fixed with regression tests.
Configurable node CPU/RAM/disk admission and image-cache quotas are implemented in
the new candidate, with durable reservations and post-wake resource checks; live acceptance pending.
Configured worker reservations/private HDD placement and partial native provider
cycles have live evidence, but the full current HTPC matrix remains open. The
post-reboot verification correction is installed after the unrelated MacBook lease
was released by its owner. Inventory staleness during host contention blocked the
next VM dispatch; a mount-bound UUID lookup correction awaits installation. See
the dated deployment checkpoint in Verification.
Remaining: node-agent with signed inventory/heartbeats, same-L2 WoL relay, durable power
intents and connection grants/connectors. Browser noVNC and physical-host desktop
are not implemented. The Intel Mac has no accepted WoL capability; the original
sleeping-Intel-Mac desktop/debug exit gate is not met.

## Phase 3 — provider coverage

Hackintosh lab coverage exists via rootless Podman/QEMU/OpenCore on the HTPC.
Bootstrap installs are manual; Virfield secures their goldens. Full libvirt-QEMU,
accepted Linux VM/Vetu profiles and mixed-primary-provider scheduling are not
implemented. Anka/Parallels are optional and require installed licensed hosts.
Each enabled provider needs its own acquire/isolation/access/release suite.

## Phase 4 — user products and Broker contract

Remaining: `/api/v3` capability-based selection, durable Target/EnvironmentProfile/
ConnectionGrant models, short-lived authorized Access channels, thin VS Code
extension, Console terminal/noVNC/catalog-driven creation and linked Broker runs,
and Broker migration from v1 template/IP assumptions to requirements/grants.
Generated VS Code workspaces and v1 Broker remain current contracts.

## Phase 5 — hardening and retirement

Required: exact-SHA CI/security/clean archive; current-client and production
entrypoint acceptance; provider/Control restart matrix; host loss, partition,
full disk, drain and soak; node resource/cache quota live acceptance; metrics/alerts; signed
artifact evidence, promotion/revocation; VM-disk recovery and upgrade/rollback.
Controller-state backup/restore has live acceptance with two native Monterey
lease/reboot cycles; runtime disks are outside that snapshot's contract.
Signed controller-binary rollback/forward also passed. Complete package/client
upgrade/rollback remains a separate gate.
Lume runtime/factory/wrapper/patch/build paths are removed; the separate
old host service is disabled and unloaded. Do not fabricate acceptance or weaken readiness to complete a gate.

## Execution records

The files under `superpowers/plans` are dated implementation recipes, not a
current checklist. Their unchecked example steps do not mean corresponding code
is absent. Current implementation and evidence are the tables above and
Verification; archived records remain available for provenance.
