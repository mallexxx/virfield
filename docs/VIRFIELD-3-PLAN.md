# Virfield 3.0 implementation plan

Reconciled 2026-10-15 against source, installed receipts and live API. This is the
maintained checklist. [Verification](VERIFICATION.md) owns test evidence and
release acceptance; [Operations](OPERATIONS.md) owns deployed commands.
The candidate branch is `codex/retire-lume-release-hardening`; `main` has not been
promoted. `0f8bc20` / `0.0.0-standard-account` is installed; a reproduced ASIF
zero-size cache correction is pending packaging/live acceptance. Documentation
commits do not install binaries or update an already-running MCP process.
The candidate is **not release accepted**.

## Decisions and current boundary

- Keep the standard VM account **`admin` / `admin`**. The operator explicitly
  rejected automatic random account-password rotation on 2026-10-15. Removing it
  is implemented and installed in `0f8bc20`; Samsung Monterey passed two full
  cycles, while the remaining provider/client matrix is open. New identities use the configured account name as the password.
  An already-standard account is authenticated without password change. Legacy
  image credentials/goldens stay intact; only a new disposable clone converts
  its known inherited account password once, then verifies authentication and
  migrates the keychain without discarding items. Unknown keychain credentials
  still fail readiness; there is no reset/replacement fallback.
- Retain separate management/caller SSH keys, fresh VM host keys, host pinning,
  inherited/cross-lease key rejection and key-only SSH. A standard console
  password does not replace these checks. Physical shared accounts are unchanged.
- Preserve existing login-keychain items, workspace, goldens, keys, configuration
  and signing/FDA identity. Define an inspected, journaled compatibility path for
  previously rotated images; never silently reset a keychain or overwrite a
  golden to make a gate pass.
- Lume runtime/factory/wrapper/build/install paths are removed. The separate old
  service is disabled/unloaded; rollback files remain. No Lume reimplementation
  is planned.
- Run CI locally; push only the working branch with `[skip ci]`. Do not merge
  `main`, publish a release or change foreign resources. Inspect unknown outcomes
  before replay; use durable keys and exact owned fixtures for authorized cleanup.

## Work order

| Order | Work | Current state | Completion evidence |
|---|---|---|---|
| 1 | Remove automatic account-password rotation | Installed; Samsung 2/2 passed; remaining matrix open | Tests and installed image/lease/reboot cycles retain `admin/admin`; SSH isolation and native GUI/new SSH key storage still pass; old-image handling preserves data |
| 2 | Resolve current macOS 27 keychain and Sonoma reboot failures | Reproduced; causes not fully established | Bounded diagnosis, targeted correction and fresh full cycles on the exact installed candidate; no repeated unchanged VM/reboot attempts treated as a fix |
| 3 | Package and accept the corrected guest policy | `0f8bc20` installed, partial acceptance; new ASIF cache fix pending | Clean exact-source local CI/security/archive checks; idle-pool install with private backup; Tart macOS 27 and both Monterey inventories plus HTPC Sonoma/Sequoia native GUI/new SSH, actual reboot/new UUID, 1920×1080, workload/artifact/RFB/release/revocation |
| 4 | Close client and production entrypoint acceptance | Owning MCP responds but serves stale guide; Restart and Broker repeat open | Actual connected clients serve current version/all four runbooks and complete their workflows; production Broker qualification/Story, independent tests, explicit denied-write and artifacts/settlement/cleanup |
| 5 | Implement the five operator feedback items below | Added to this plan; not implemented | Each item's tests, live evidence and updated runbooks; verified-golden transfer can unblock the storage-constrained iPad repeat in step 3 |
| 6 | Complete remaining hardening/debug/DR gates | Partial dated evidence only | Agreed scope below accepted separately; no promotion based solely on unit tests or another OS/version |

The full current-candidate acceptance, verification and branch push precede general
follow-up delivery. A narrowly needed dependency, such as verified storage
transfer for the iPad repeat, may be implemented earlier and must get its own
acceptance. The latest instruction requires documenting all five items now.

## Current evidence and open defects

| Area | Verified state as of 2026-10-15 |
|---|---|
| Service/install | `0f8bc20` installed; approved Background → Standard migration retained; signing identity/config/keys/seven baseline images preserved; exact installed HTTP/fresh stdio checks passed |
| Native Apple browser | Installed layout, Safari URL handoff and real Apple sign-in page passed using a synthetic link; no account login or authenticated download completion tested; Mach-O minimum 13 verified, no live macOS 13 host test |
| HTPC Sequoia | Two full `f60efe3` + Standard native GUI/new SSH/reboot/display/workload/artifact/RFB/release cycles passed; these predate removal of password rotation |
| HTPC Sonoma | Initial native GUI/new SSH/display passed; no fresh boot UUID or returning SSH within the full 1,200-second reboot budget; owned fixture released, bounded logs/frame retained; cause unproven |
| macOS 27 | Two standalone `f60efe3` attempts failed native login-keychain authentication with `-25293`; isolated native API fixtures passed in the same guest; original-copy candidate authentication failed; no established historical password/corruption cause; owned fixtures released |
| Samsung Monterey | Two complete `0f8bc20` standard-password/native/reboot/Xcode 13.4.1/Swift/workspace/artifact/release cycles passed |
| iPad Monterey | Earlier complete native/reboot and Xcode/Swift receipts retained; fresh repeat storage-constrained, not accepted on the new policy; Monterey here is macOS 12.7.6, not macOS 13 |
| Factory/private GHCR | Complete fresh public-base factory → private portable push → pull → clone workload → cleanup passed on `2f23ae6`; preserve receipt, repeat only for relevant pipeline changes; no managed golden uploaded |
| Restore/rollback | Live controller-state restore and signed controller-binary rollback/forward passed; VM disks and complete package/client rollback remain outside those receipts |
| Actual owning MCP | This chat responds but serves an older guide; requested Restart pending; exact `0f8bc20` HTTP/fresh stdio passed |
| Existing HTPC file modes | Optional owner-only permission proposal unapproved; files unchanged; not required for VM functionality and not a keychain/reboot fix |

The new `0f8bc20` existing macOS 27 clone still fails login copy-auth and was
released. Fresh same-pinned security-v1 and exact UI-test goldens passed native
post-reboot qualification; worker acceptance exposed cached ASIF disk size zero
versus a fresh CLI 140 GB. A red/green-tested cache correction awaits installation.
The new HTPC Sequoia attempt failed because an unrelated speech-dispatcher log
filled `/run/user/1000`, preventing Podman metadata writes despite free HDD space.
Its exact inspected partial clone was resolved/released; foreign log cleanup
awaits explicit approval. Existing production macos27 routing/golden and seven
baseline manifests remain intact; the separate new UI-test golden is retained.

Older successful Tart/macOS 27/HTPC matrices remain dated evidence, not proof that
these newer failures are fixed. Exact receipts and diagnostic boundaries are in
[Verification](VERIFICATION.md).

## Operator feedback backlog

All five items were explicitly requested on 2026-10-15. They are delivery work,
not existing API promises. Order below follows recovery/diagnosis, resource
explanation, image portability and optional toolchain support.

### F1 — recover a drifted lease without deleting its VM

Current transient unavailable-host/unknown-address handling preserves a ready
lease and verified streams. Once two verified contradictory observations mark
`vm_drift`, however, there is no operation to return that lease to `ready`.
The existing operator `resolve` schedules cleanup; it is not recovery-in-place.

Implement an explicit durable revalidation operation for the exact owned,
unexpired lease. Check runtime identity, image/provenance, running state, SSH
host pin and existing management/caller authentication before restoring access.
Preserve lease/VM identity, workspace, keys and deadline; do not rerun normal
preparation, rotate credentials, clone or delete. A new address may be accepted
only after identity and pinned SSH are reverified. Deny ambiguous/in-flight,
released/expired, foreign or mismatched targets. Test lost replies, replay,
controller restart, changed identity and expiry races. Live acceptance must
retain a workspace checksum through a reproduced drift/recovery cycle.

### F2 — retain drift and tunnel-close diagnostics

Current drift warnings log expected/observed addresses and state, but the durable
event is generic and endpoint fields are cleared. Tunnel close/copy errors have
no durable cause record. The historical Monterey drift's exact cause is unknown.

Persist bounded expected/observed identity, IP, SSH port, state, observation time
and original backend error before transition. Record listener pause/resume/close
and stream termination separately, including explicit close, release, expiry,
verified drift, daemon shutdown and upstream I/O failure. Expose safe reasons in
API/events/help; private detail must omit credentials, desktop URLs and payloads
and survive owned-fixture cleanup. Test each cause and restart persistence. Never
invent a historical cause from later screenshots or successful SSH.

### F3 — explain resource admission arithmetic

Status already exposes limits, reservations and free bytes; primary CPU/RAM
errors include partial arithmetic. Disk refusal and node/cache errors do not
explain the whole per-candidate calculation.

Return and display the admission snapshot per host/storage: observed free,
already reserved growth, requested full potential size, safety reserve, remaining
headroom and failed constraint, using exact bytes plus explicit GB/GiB units.
Cover CPU/RAM, overlay/golden storage and image-cache limits; unknown metrics
must identify what is unavailable. Keep admission/recheck calculations consistent.
The reported historical 251 GB is not enough to establish that rejection's cause
without its contemporaneous reservations and storage selection. Test boundaries,
multiple destinations, concurrent reservations and CLI/MCP/Console explanations;
do not lower reserves to make a workload fit.

### F4 — transfer a verified golden between local storages

Current Tart acquire refuses cross-container clones before reservation. Registry
pull/publication is not a local managed-golden transfer and must not upload one.
The operator had to rebuild Monterey/Xcode in another container (~39 minutes).

Add an explicit journaled copy/transfer to a new destination/name, preserving the
source by default. Check full destination capacity and source-not-in-use rules;
preserve image/recipe provenance, OS/build/security/toolchain verification and
private identity mapping. Verify copied contents/hashes and destination runtime
identity before atomic registration; invalidate or repeat verification if an
invariant changes. Keep partial copies quarantined and handle interruption/replay
without overwriting a target. Any source deletion is separately authorized.
Accept with an actual Monterey/Xcode transfer, fresh native/reboot/workload lease,
artifact hash, cleanup and unchanged source; this can unblock iPad storage.

### F5 — optional legacy iOS build profile

Current Xcode qualification/Swift smoke is not a pinned Ruby/CocoaPods or legacy
iOS dependency profile. Add an opt-in profile for the verified Monterey
12.7.6/Xcode 13.4.1 combination; leave ordinary profiles unaffected. Install/check
Rosetta only on Apple Silicon, pin a compatible Ruby/CocoaPods and native gem
architecture, and verify actual `require`/native-library loading for FFI/Ethon.
Record versions/architectures/provenance and run a representative legacy iOS
armv7 build with lockfiles. Report unsupported host/toolchain combinations
explicitly. Prior successful iVim/Notepad builds are historical workload evidence,
not implementation of this reusable profile.

## Remaining 3.0 architecture

These phases retain the original target scope. They are not all implied to be
mandatory independent processes or already supported release features.

| Phase | Implemented slices | Remaining scope / exit gate |
|---|---|---|
| 1 — Tart and Orchard | Tart lifecycle/inventory/Console, account parameterization, SSH isolation, configured Cirrus/Packer adoption, dynamic factory/GHCR; dated Broker v1 smoke | Current guest-policy defects/acceptance above; Tart VS Code debug; Orchard isolated access/factory/live pool acceptance. Tart and Orchard cannot share one daemon's primary provider |
| 2 — nodes and access | R1 pinned-SSH physical host leases/wake, R2 rootless Podman macOS VMs, R3 VM Screen Sharing; host exclusivity/sleep holds/cleanup; resource/cache admission and private HDD placement | Current HTPC/resource/quota matrix; signed node-agent inventory/heartbeats, same-L2 WoL relay, durable power intents, grants/connectors; physical-host desktop and browser noVNC. Intel Mac WoL capability/sleeping-Mac debug exit gate unaccepted |
| 3 — provider coverage | HTPC QEMU/OpenCore lab via rootless Podman; manual bootstrap | Full libvirt-QEMU, accepted Linux VM/Vetu profiles, mixed-primary-provider scheduling; optional licensed Anka/Parallels only on installed hosts; independent isolation/access/release acceptance per provider |
| 4 — user products and Broker | Generated VS Code workspaces and v1 template/IP Broker contract | `/api/v3` capability selection, durable Target/EnvironmentProfile/ConnectionGrant, short-lived Access, thin VS Code extension, Console terminal/noVNC/catalog creation/linked Broker runs, Broker requirements/grants migration |
| 5 — hardening | Local CI/security/archive/install slices, durable jobs/admission/cleanup, controller-state restore and controller-binary rollback; Lume retirement | Production clients/entrypoints, restart-during-build/provider crash matrix, host loss/partition/full disk/drain/soak, resource/cache live limits, metrics/alerts, signed promotion/revocation, VM-disk DR and complete package/client upgrade/rollback |

## Records and completion rules

The [original detailed design](history/VIRFIELD-3-PLAN-before-2026-10-14.md),
[dated verification checkpoints](history/VERIFICATION-checkpoints-through-2026-10-15.md)
and files under `superpowers/plans` preserve provenance. Their old requirements,
unchecked recipe steps and pending statuses do not override this checklist or the
operator's standard-password decision. Record source/package/client/test scope
separately. Mark work complete only after implementation, relevant local tests,
live acceptance, documentation and working-branch push; keep follow-up automation
active while required work or an external gate remains open.
