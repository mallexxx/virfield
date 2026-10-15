# Verification

Current record, reconciled 2026-10-15. The release candidate is **not accepted**.
Local Tart, SSH physical nodes and rootless Podman macOS lab VMs are implemented;
acceptance belongs to the exact versions and paths below. Source, installed
package, running MCP processes and published release are distinct artifacts.
The working branch is `codex/retire-lume-release-hardening`; `main` is not promoted.
The [implementation plan](VIRFIELD-3-PLAN.md) owns delivery order and all five new
operator feedback items.

## Standard password policy — source implemented, acceptance pending

The operator requires the standard VM **`admin` / `admin`** account and explicitly
rejected automatic random account-password rotation. Candidate source now creates
standard account credentials and never changes an already-standard password. A
new disposable clone of an old image authenticates its inherited password before
a one-time conversion to the standard password. Existing golden credentials are
never silently rewritten; in-place securing of a legacy golden is refused before
mutation. Installed `f60efe3` still uses the old policy. The new policy has not yet
been packaged, installed or accepted.
Existing secured images may retain generated credentials; do not assume they
already accept the standard password or modify them outside a journaled migration.

Local source checks on 2026-10-15 passed: complete `make check` (17 Python
tests, Go vet and full race suite), Staticcheck 0.7.0, module integrity,
Govulncheck 1.6.0 (zero reachable vulnerabilities) and Gitleaks 8.30.1
(233 commits). New stateful shell regressions cover unchanged standard accounts,
one-time legacy-clone conversion, wrong inherited credentials, random targets
and ineffective password changes; credential tests preserve legacy journals and
independent SSH keys. These are source checks, not installed/native acceptance.

Keep management/caller SSH keys, fresh host keys, host pinning, inherited/cross-lease
key rejection and key-only SSH. Preserve login-keychain items and workspaces;
resetting/deleting a keychain or replacing a protected golden is not acceptance.
New policy acceptance must cover image preparation, worker creation and actual
reboot, standard account authentication, automatic login and native GUI/new SSH
`WhenUnlocked` add/read/data comparison/delete, plus display/workload/artifact/
RFB/release. Older passing cycles below used the previous password policy.

## Acceptance checkpoint (2026-10-15)

| Artifact / operation | Evidence and boundary |
|---|---|
| Installed source | `f60efe3310cd3806c18481b5d22089c421849f8b`, version `0.0.0-browser-transport-trace` |
| Archive | `virfield-v0.0.0-browser-transport-trace-darwin-arm64.tar.gz`; SHA-256 `941da8a267107103918ddc553290419f104148f5bebb7503917b130328265c00` |
| Local checks for that code | Complete `make check`: seventeen Python tests, documentation links, formatting, Go vet/full race suite; pinned Staticcheck 0.7.0, Gitleaks (229 commits), clean archive/extraction/signature/config smoke passed |
| Installed provenance | Private backup, prior signing/FDA identity, unchanged configuration/keys and all seven baseline image manifests verified; receipt `upgrades/*-f60efe3/installed.json` |
| Installer correction boundary | Binary replacement succeeded; the private installer's final predicate incorrectly required a deleted disposable template. Read-only attestation completed without repeating installation/restart |
| Service migration | Operator completed the separately approved Background → Standard migration; only plist `ProcessType` changed; fresh daemon PID 65903 and installed signed hashes verified |
| Exact installed MCP smoke | HTTP/fresh stdio version, all four runbooks/resources, discovery/status and anonymous 401 passed: 1.22 seconds before migration, 0.18 seconds after |
| Owning release-task MCP | Actual help still returns `Transport closed` after user-confirmed Restart; rechecked during this documentation reconciliation. Fresh stdio and Broker's separate working client do not close this gate |
| Latest cleanup | Both failed owned macOS 27 fixtures released with original journaled keys after bounded diagnostics; API then reported zero worker leases/jobs, capacity 0/2 and seven image records |
| Lume | Runtime/factory/wrapper/build/install paths removed; old separate service disabled/unloaded; latest retained check at 14:07 UTC confirmed port 7777 closed; rollback files preserved |

This documentation revision changes embedded help in the next build only. The
running `f60efe3` package still serves its own older embedded runbooks. New code
requires fresh exact-source local checks and a clean package; no source-only
change is an installed fix. Remote CI remains deferred by the operator.

### Current native provider results

| Profile / tested code | Result | Scope and remaining work |
|---|---|---|
| HTPC Sequoia / `f60efe3` + Standard | **PASS 2/2** full fresh cycles | Native Aqua and new SSH `WhenUnlocked` I/O, unlocked unobstructed console, 1920×1080, actual reboot/new boot UUID/nonces, retained SSH pin, RFB before/after, workload/artifact hash, cross-key rejection, release and tunnel revocation; repeat relevant gates after account-policy change |
| HTPC Sonoma / `f60efe3` + Standard | **FAIL reboot** | Initial readiness/native GUI/new SSH/1920×1080 passed. Following one reboot dispatch, no returning SSH or new boot UUID within the full 1,200-second budget. QEMU/container stayed running, guest SSH banner was absent even from node loopback; bounded logs/frame retained, fixture released and listeners revoked. Exact stall cause unproven |
| macOS 27 / `f60efe3` | **FAIL native login authentication**, twice | Aqua agent exited 65 in login-rekey; current/previous/user candidates returned `-25293` at copy-unlock. Display passed, but native readiness and workload did not. Both owned fixtures released; no worker reboot or second successful clone claimed |
| Samsung Monterey / `614daf5` | **PASS 2/2** full cycles | Native GUI/new SSH, real reboot/new UUID, 1920×1080, Xcode 13.4.1/13F100 Swift compile/run, workload/artifact/RFB/release/revocation; historical policy, new policy untested |
| Fresh public-base Ventura factory / `614daf5` | **PASS** qualification + two full clone cycles | Native/reboot/workload/artifact/RFB/release passed; own factory/workers deleted, seven baseline manifests retained; no registry push repeated |
| iPad Monterey / `6779b6f`, `8c521fe` | **PASS** earlier native/reboot and exact Xcode/Swift receipts | Fresh repeat blocked by internal storage. Observation at 13:52 UTC: about 75.7 GB free versus about 150 GB full-growth-plus-reserve requirement; this is a dated snapshot, not current free capacity. No reserve reduction or foreign cleanup. Cross-container Tart clone is currently refused |
| Tart four-profile matrix / `4a53b67` | **PASS 8/8** earlier | iPad/Samsung Monterey, Ventura and macOS 27, two native/reboot/display/workload/artifact/RFB/release cycles each; does not override newer macOS 27 failures |

Monterey in these records is **macOS 12.7.6 with Xcode 13.4.1**, not macOS 13
(Ventura). Neither historical workload success nor an image-ready record accepts
the candidate standard-password implementation, which is not installed yet.

Private receipt directories under `/private/tmp` retain request keys, exact owned
identities and bounded evidence:

| Result | Receipt directory |
|---|---|
| Sequoia first / second | `virfield-htpc-sequoia-diagnostic-f60efe3-20261015`, `virfield-htpc-sequoia-second-f60efe3-20261015` |
| Sonoma reboot failure | `virfield-htpc-transport-diagnostic-f60efe3-20261015` |
| macOS 27 failure / follow-up diagnosis | `virfield-macos27-native-f60efe3-20261015`, `virfield-macos27-auth-diagnostic-f60efe3-20261015` |
| Samsung Monterey / public factory | `virfield-installed-samsung-monterey-614daf5-20261015`, `virfield-installed-native-factory-614daf5-20261015` |
| Earlier iPad native / toolchain | `virfield-installed-ipad-monterey-6779b6f-20261015`, `virfield-installed-ipad-monterey-8c521fe-20261015` |

### What keychain and transport research actually established

On the retained macOS 27 diagnostic guest, five isolated native fixtures passed,
including real copy authentication/cache refresh and existing-item preservation.
The original login keychain reported unlocked (flags 7), but copies rejected all
examined known candidates with `-25293`. Guest current/previous identity values
matched controller/source-image credentials; six retained image-credential files
contained one distinct source secret. Original keychain bytes/default/search list
were preserved. These observations do **not** distinguish an unknown historical
keychain password from corruption, establish that the native APIs are generally
broken, or prove that Standard migration fixed this profile. No original
keychain reset or full keychain export to the host was performed.

A separate earlier Monterey diagnosis did establish inconsistent cached state:
the original rejected native authentication and SecItem access while a private
in-guest copy accepted the same current password. One validated native cache
refresh restored I/O and preserved items. Its `614daf5` correction has five native
fixture tests and the dated Monterey/factory acceptance above. That evidence does
not prove the same cause for macOS 27. Removing account rotation is now explicitly
required; cache research does not override that product decision.

The older inherited-desktop SSH failure hit a 60-second command timeout before
worker credentials existed; later read-only SSH succeeded. Installed `f60efe3`
adds bounded private channel-open/exec-acknowledgement/command-exit trace, covered
by real stalled-SSH regressions. Fresh Standard-era Sonoma/Sequoia desktop probes
completed in about 124/153 milliseconds. The old timeout was not reproduced in
those cycles; its exact cause and causal relation to Standard remain unproven.
The current Sonoma reboot failure is a separate full-budget failure, not the
older private verifier's prematurely aborted single reconnect attempt.

Historical Monterey `vm_drift` and tunnel closure still lack a specific proven
cause. Current source logs some expected/observed drift fields but persists a
generic event, clears the endpoint and offers cleanup-only operator resolution.
Recovery-in-place and durable tunnel-close explanations are planned F1/F2 work,
not capabilities supplied by the transient-inventory preservation fix.

### Broker macos27 reacceptance

The Broker `macos-codex` profile uses Tart template `macos27`. Three production
qualification attempts failed `guest_keychain_failed` before bootstrap/provider
execution; the last Broker-tested candidate was `614daf5`. All three owned leases
were released. Exact run/job/lease bindings, deployment provenance and timestamps
remain in the private Obsidian incident register and Broker evidence.

Broker has not repeated its production macos27 path on `f60efe3` after Standard
migration. The two standalone `f60efe3` failures above establish native login-auth
failure on that deployment; they are not Broker Story runs and do not identify
the old three attempts' unrecorded substage. The generic public error alone proves
no particular password, OAuth or cache cause. Broker's own working MCP help/status
is likewise a separate client from the release task's closed transport.

Close this gate with a fresh worker on the corrected exact installed candidate:
pinned SSH, native GUI/new SSH key storage and console qualification, production
Broker bootstrap/Story, independent tests, explicit Codex read-only write denial,
artifact hashes, credential settlement and confirmed release. Preserve bounded
private diagnostics before cleanup within the existing deadline; keep public
job/lease error evidence if private detail is unavailable. Do not indefinitely
retain failed VMs or repeat unchanged attempts as proof of a fix. See
[VM keychain operations](OPERATIONS.md#vm-keychain-readiness).

## Completed receipts to preserve

| Gate | Tested result / limits | Private receipt |
|---|---|---|
| Factory/private GHCR | `2f23ae6`: fresh pinned public-base factory qualification/two clone workloads, separate fresh sanitized portable build, authorized private push/visibility/digest, pull/qualification/two clone workloads/artifacts/RFB/release, own-image cleanup; seven baseline manifests preserved; no managed golden uploaded | `virfield-release-roundtrip-20261014/journal.json`, result stage `roundtrip` has `passed: true` |
| Registry identity | Private package `mallexxx/virfield-hardening-acceptance`, tag `release-check-0644cb90dac8`; digest `sha256:a5d42b4e6e48641fba1af7171158de39252e4439d3087c1b019f392223054e73`, 21,460,835,255 descriptor bytes; registry evidence retained | Same durable roundtrip journal |
| Live controller-state restore | Restored private SQLite/images/node identity with new owner token; one manager/listener, old token 401, seven manifests intact, exact MCP and two native Monterey reboot/workload/release cycles; byte-exact original config/state returned | `virfield-live-restore-drill-20261015/report.json` |
| Signed controller-binary rollback | `2724f94 → 2f23ae6 → 2724f94`, fresh processes/versions/hashes, original config and images verified; unknown probe inspected before resuming | `virfield-controller-rollback-drill-2724f94-20261015/report.json` |
| Service migration | Only ProcessType Background → Standard; hashes/config/keys/seven images unchanged; exact HTTP/fresh stdio passed afterwards | `virfield-standard-migration-external-verification-20261015.json` |
| Native browser GUI | Fresh installed `f60efe3`, Safari URL handoff, real Apple sign-in page, controls visible with long URL; fake expired link handled; test app/tab closed | `virfield-native-browser-gui-f60efe3-20261015.json` |

Repeat a completed gate when relevant behavior changes; do not rerun a registry
upload, state restore or rollback merely because an unrelated gate remains open.
Controller-state restore excludes VM disks. Controller-binary rollback excludes
complete package/client rollback. Registry publication must always use a fresh
portable public-base build, never a private managed golden.

The browser GUI receipt used a synthetic non-authenticating link; no Apple account
credentials were entered or authenticated Xcode download resumed. Cookie handoff
and actual download completion remain a distinct untested flow requiring a real
pending download and operator sign-in. The native browser now explicitly targets
the plist's macOS 13 minimum; Mach-O metadata/build check passed, not a live macOS
13 host test. Apple login is not required to repeat the synthetic GUI smoke.

## Remaining release acceptance

| Required work | State / closure criterion |
|---|---|
| Standard VM password implementation | Source implemented; package and accept it; test/preserve `admin/admin`, retained keychain data and independent SSH isolation; define compatible handling of previously rotated images |
| Current native VM matrix | Diagnose/fix macOS 27 keychain and Sonoma reboot; build/install exact clean corrected package in idle pool and pass relevant Tart/HTPC cycles; Sequoia old-policy 2/2 does not accept new policy |
| iPad repeat | Restore adequate full-growth-plus-reserve storage or accept verified-golden transfer first; no reserve weakening or foreign deletion |
| Owning clients and Broker entrypoint | Current actual MCP version/runbooks/workflows; Broker macos27 criteria above; fresh stdio/HTTP alone insufficient |
| Apple authenticated download | Real exact-job sign-in/cookie handoff/download completion untested; synthetic GUI smoke already passed |
| Release engineering | Local checks/security/archive/install provenance for exact new source; complete package/client upgrade/rollback, not only controller binary drill |
| Failure/debug/operations | Tart VS Code debug; restart during build/provider crash matrix, host loss/partition/full disk/drain/soak, live node resource/cache boundaries, metrics/alerts, signed promotion/revocation and VM-disk DR |

Existing HTPC golden chmod is an **optional hardening proposal**, not a functional
prerequisite. New conversions commit owner-only files; the two old qcow2/OpenCore
pairs remain 0444 under a 0755 directory. The operator asked why, did not approve
that change, and no chmod was performed. It does not fix keychain authentication
or reboot and must not be presented as the reason functional acceptance is open.

All five requested follow-ups are in [the maintained plan](VIRFIELD-3-PLAN.md):
non-destructive drift recovery, durable drift/tunnel causes, resource arithmetic,
verified local golden transfer and an optional legacy iOS toolchain profile.
They are not yet implemented. Orchard live pool/isolation/factory/access, broader
v3 grants/Access/node-agent/extension/noVNC and additional providers remain roadmap
scope, not silently enabled features of this candidate.

## Not yet accepted

In addition to the current release gates above, no acceptance is claimed for
Orchard worker restart/drain or mixed-primary-provider scheduling, remote/container
TLS through the intended production entrypoint, full apple-browsers UI-suite
through Broker, physical-host desktop, Vetu/Linux VM coverage or the whole v3
roadmap. Earlier DDG UI smoke and Broker v1 smoke have narrower dated scope.

## Historical evidence

[Checkpoint archive through 2026-10-15](history/VERIFICATION-checkpoints-through-2026-10-15.md)
preserves the previous detailed investigations, fixed-defect table, package hash
ledger, DDG/CI findings and R1–R3 receipts. Its then-pending statuses and password
rotation descriptions are superseded here; no old passing result is erased.
[Pre-2026-10-14 records](history/VERIFICATION-before-2026-10-14.md) include the retired
Lume deployment. Dated plans/specs remain provenance, not current runbooks.

## Release procedure

Use a clean committed revision and run local CI/security/release checks for that
exact source. Build with `tools/release.py NEW_VERSION --output NEW_DIRECTORY`;
it packages fresh binaries/native browser, notices, hashes and revision manifest.
Validate extraction, signatures, current configuration, URL scheme, idle-pool
installation, actual clients, native workflows and confirmed cleanup. Preserve
private backup, signing/FDA identity, configuration, keys and existing goldens.
Never overwrite outputs/tags or replay unknown mutations blindly. The
[live acceptance runner](../tools/live-acceptance.py) retains durable private keys
and request journals and requires authorization for its disposable cleanup.

This task authorizes working-branch push with `[skip ci]`, not merge/tag/release.
The historical published `v2.0.0` is a Lume archive, not this Tart implementation.
Keep unfinished feature delivery separate from verified readiness of current code.
