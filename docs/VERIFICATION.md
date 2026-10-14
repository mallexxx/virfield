# Verification

Current record, reconciled 2026-10-14. Accepted deployment: local Tart plus SSH
nodes and Podman macOS lab VMs. Virfield switched to Tart on 2026-10-08. On 2026-10-14 the retired separate
Lume LaunchDaemon was disabled and booted out after verifying empty inventory; its rollback files are preserved and port 7777 is closed.
[Earlier Lume acceptance and release records](history/VERIFICATION-before-2026-10-14.md)
are historical evidence, not current gates. Later passing tests do not invalidate
newly reproduced defects below. Source, installed candidate and published release
are distinct artifacts.

## Candidate hardening (2026-10-14)

Candidate `09ad696` was installed on 2026-10-14 with the same Developer identity and
Team ID E3R68ED529, preserving all five image-ready records. Local `make check`
passed and six successive clones (two per configured Tart profile) passed pinned
SSH, cross-key rejection, distinct host keys, exact OS/build/SIP, desktop, artifact
export and confirmed release. Disk reserve correctly prevents two concurrent
full-size disks on the external SSD; this run used sequential clones.
Supplemental candidate `aee89c1` is now installed. HTTP and fresh stdio MCP passed
help/status/tool discovery, including HTTP 401 without authentication. Two fresh
stdio MCP factory-clone cycles passed acquire/replay, pinned SSH workload,
artifact export, desktop and confirmed release. The live discovery check now
requires named workflow tools rather than the obsolete hardcoded count of 17.
A Ventura live SSH suite passed exact Xcode/Swift
workload, artifacts, desktop revocation and restart with the same lease SSH identity.
The new automation factory rebuilt and qualified image
`image-5387f340da4ae093162383c18449c592`; two sequential clones on its explicit
internal container passed pinned SSH, cross-key rejection, distinct host keys,
OS/build/SIP, desktop, artifact export and confirmed cleanup.
The published `d2daf4e` storage-routing candidate is packaged but not installed:
the operator elected to wait for the unrelated active macos27 lease to be released.
Node clone acceptance and the private GHCR roundtrip are still outstanding.
The older service snapshot below is a dated checkpoint.

| Audit defect | Candidate implementation and verification |
|---|---|
| HTPC host exclusivity | Separate one-host reservation, VM-pool limit and atomic node host lock; control regression tests |
| Shared node sleep holds | Node-level reference set, foreign-hold preservation, last-reference cleanup; two-VM/failure fixtures |
| Node image cleanup | Provider/guest preserved, golden backing protected, failed Podman cleanup retains overlay/hold; regression tests |
| Tart image creation | Approved digest-pinned base catalog, correct default location, base/developer/automation recipes; profile/catalog/build tests |
| Generic automation | Guest apply + post-reboot signed TCC/AMFI/Gatekeeper verification; new live factory qualification and two sequential clone workloads passed |
| Xcode authorization | Shared resumable verified archive store and exact-job Tart resolve sign-in recovery; download/auth tests |
| Registry | Tart flat/ASIF manifest validation, pinned pull, fresh sanitized portable export and confirmed cleanup; fixtures and live public metadata |
| Installer | Explicit Tart init, three-binary service generator, separate private state dir and optional browser; Go/Python tests |
| Orchard readiness | Daemon now always requires per-lease SSH preparer; source-only remote templates fail admission before creation; remote image/access/live-pool acceptance remains absent |
| Lume retirement | Runtime/factory/wrapper/patch/build/install code removed; implicit legacy runtime and image_tools rejected; old separate service disabled/unloaded after empty inventory check |
| Host identity collisions | Config and merged inventory reject overlapping primary/node host IDs; reproduced regression tests |
| Image resource admission | A pending image build was reproduced bypassing worker CPU/RAM/disk admission; builds/exports now reserve before dispatch, count in quota and refuse unknown macOS 27 disk bounds; regression tests |
| Node identity backup | Private management keys/probes included, unsupported files abort snapshot; backup fixtures |
| Node resource/cache admission | Configurable per-node VM budgets, worker/build reservations, qcow2 virtual sizes and storage probes, post-wake clone/start/commit checks; regression tests; live acceptance pending |
| Node overlay storage | Live quota probe exposed only 99 GiB free on HTPC home NVMe; dedicated `vms.storage_dir` supports new VM overlays on data storage, keeps physical workspaces separate and blocks changing roots with overlays; live acceptance pending |
| Existing node golden permissions | New commits use owner-only mode 0400; the two older secured HTPC qcow2/OpenCore goldens still have mode 0444 under traversable directories. Their permission migration awaits separate operator approval; files are unchanged |
| Tart clone routing | A factory golden on internal storage selected the deployment's external default; source-container compatibility is now checked before reservation, and the acceptance runner selects each golden's own container |
| Interrupted Tart inventory | Regression tests reproduced canceled address/container probes returning partial healthy snapshots; refresh now fails on cancellation and Control preserves the last complete inventory while pausing admission; supplemental installation pending |
| Cold Tart transfer | First live factory attempt failed with Packer EOF during source transfer; journaled recovery deleted only its own failed record after process/inventory inspection. New candidate prefetches the pinned digest in retryable resolve and disables automatic cache pruning; roundtrip not yet accepted |
| GUI/SSH keychain | Private Aqua/SSH session preparation, retained/rekeyed login items, native WhenUnlocked add/read/delete and fresh boot evidence; Ventura/macOS 27 live fixtures and real DDG CI Downloads smoke passed |
| UI desktop | Native persistent 1920×1080 logical mode in Aqua, host-side Tart configuration and no window refit; real Settings/General UI-test smoke passed |
| macOS 27 ASIF factory | Explicit Recovery keep bypasses default deletion/file lock; full isolated source factory build/qualification passed |
| SSH boot banner | 45-second banner budget, timeout classified as retryable connection failure before commands; real local SSH banner regression test |
| Go security | Minimum Go 1.26.9; govulncheck finds zero reachable or imported-package vulnerabilities |

Pinned Staticcheck v0.7.0 passes. Govulncheck v1.6.0 has one module-only notice
GO-2026-5932 for unmaintained `x/crypto/openpgp`; that package is not imported and
has no fixed version. Virfield uses `x/crypto/ssh`. The old Go 1.26.8 reachable
findings are resolved by the patch upgrade.

The previous [c41c04b CI](https://github.com/mallexxx/virfield/actions/runs/38273172551)
failed on both OS runners because the retired image factory imported Pillow
without installing it. That factory, its Python tests and the Lume build step
are removed. This is a CI source fix, not a fresh passing GitHub run.

Public live Tart manifest resolution passed for `cirruslabs/macos-ventura-base`,
digest `sha256:9fb387bb987cdda942fbc27118296ed8e16a3359a672c95c6d5ac960165ef1c5`,
compressed descriptors 22,617,320,232 bytes. It downloads no disk and uploads
nothing. Packer syntax-only validation passes for base/developer variants,
including macOS 27 variants without Recovery relocation.

## DDG keychain and desktop regression (2026-10-14)

The DDG `apple-browsers.git` CI runner creates `DefaultKeychain` with password
`default`, sets it as default, unlocks it and disables sleep locking before
running UI tests (`macOS/fastlane/Fastfile`, `create_keychain_ui_tests`). Its
`EncryptionKeyStore` writes a native generic password with `WhenUnlocked` and
reads it by service/account. An SSH unlock does not establish GUI audit-session
access, and a GUI unlock does not establish access for each new SSH session.
The old preparer rotated account/autologin credentials without a keychain gate.

The candidate adds an Aqua login agent, private per-image/lease default keychain,
content-preserving login-keychain rekey, and an SSH ForceCommand bridge that
unlocks immediately before each command. Native add/read/data comparison/delete
must pass in both contexts; qualification checks a fresh post-reboot proof.
The login keychain remains searchable so ScopedBookmarkAgent cannot leave an old
password dialog over the browser. Passwords are not made blank or public.

Tart recipes/clones configure a 1920×1080 logical desktop. Cached guest modes
ignore a host display hint, and `CGDisplaySetDisplayMode` reverts when its process
exits. The Aqua agent uses a permanent CoreGraphics configuration transaction and
checks the actual frame. macOS 27 recipes also explicitly keep Recovery: the
Packer 1.21.0 default deletion opens an ASIF file and retains its lock, reproducing
Virtualization.framework startup failure. A new source factory build with `keep`
completed its Packer build and post-reboot qualification.

Live evidence uses separately journaled disposable fixtures; production leases
were untouched. Ventura native GUI key I/O and reboot passed. On macOS 27.0 /
26A428, native GUI and SSH key I/O passed before/after actual reboot, as did the
new source `resolve → build → qualify → stop` factory. Source per-lease preparation on its own clone also passed caller/management/host-key isolation, private login-keychain rekey, native GUI/SSH access and real worker reboot; its guest identity file retained only the new credential. Two successive runs of the
real DDG CI `DownloadsUITests.testDownloadFinishesThenPopupIsShown` passed
(1 test / 0 failures each), including browser launch and Settings/General clicks.
Host products built with Xcode 27.1 ran with guest Xcode 27.0 / 27A266a. The second
run used the candidate guest agent after reboot and no manual unlock. This is a
single-test smoke, not acceptance of the entire DDG UI suite or all Xcode minor
version combinations. SCP upload/download worked with the SSH bridge.

Private journals and result bundles are retained under
`/private/tmp/virfield-keychain-macos27-standalone-20261014`;
`factory-journal.json` identifies the exact source recipe digest and owned image.
Two failed old-daemon factory attempts were deleted through exact-name journaled
recovery after confirming no VM/process remained. No existing golden was replaced.
These fixes are not installed in the production daemon yet: installation still
waits for the operator-selected release of unrelated active leases. Native tool
exit status alone is never the new readiness gate.

## Automated candidate results

The complete `make check` passed: documentation links, seven Python tests,
formatting, `go vet` and the full Go race suite. Three Go binaries and the native
Apple browser built; the browser signature verified. The existing installed
configuration passes the candidate CLI `config-check`. Pinned Staticcheck, module
integrity and current-tree Gitleaks pass; govulncheck results are recorded above.
Live mutation tests remain opt-in and were run separately as recorded above.

Clean local package provenance:

| Source revision | Archive SHA-256 | Deployment |
|---|---|---|
| `aee89c1ffafb01faccc750b60d0e3470bf14fc97` | `2313602b2c380e5e74640c47ee6ad50d9c10d750f856a0949833a4489803cf35` | Installed; signed binary hashes retained in the private upgrade record |
| `d2daf4ee613739686e2227a29e513d3e1a592c9b` | `6d4a01acb5baddf3d441f5c6f5e557125046ce45f74d4ccf03b37c1c46039c2b` | Packaged; installation waits for lease release |

The supplemental interrupted-inventory fix is newer than both packages.

Offline restoration of `backup-5cdef526c555f6a53ed5f565c6e2edaf` passed into a
separate private directory: 18 copied files, SQLite integrity, all five active
image identities and their credentials/verification, unchanged node management
key, rotated owner token and configuration validation. No restored daemon was
started; live restore/upgrade/rollback acceptance remains outstanding.

[Live acceptance runner](../tools/live-acceptance.py) persists request keys before
dispatch, retains a private journal, and deletes only its own accepted disposable
leases after explicit cleanup approval. Its no-approval and lost-reply behavior
are tested. The Go `TestLiveLeaseSSH` also uses the actual guest user/port, accepts
Tart APFS encryption at rest with FileVault off/unlocked, and checks desktop
revocation alongside SSH, cross-key/image-key rejection, workload and artifacts.

## Remaining release acceptance

- Repeat exact-source local CI and clean packaging for subsequent code changes;
  remote CI is deferred by the operator. Existing package provenance is recorded above.
- Install the latest supplemental candidate after the unrelated lease is released and accept node resource policy with disposable
  node clones; preserve the passing `09ad696` Tart clone evidence above.
- Complete permission migration for existing HTPC secured goldens after approval;
  separate authorization is still pending, and read-only inspection confirmed mode 0444.
- Complete GHCR publication with real portable rebuild, push/pull-back/clone
  workload; the factory/automation rebuild and clone workloads already passed.
  The operator authorized an existing GHCR identity
  and new private `mallexxx/virfield-hardening-acceptance` package; a dedicated
  source and temporary internal Tart container are configured. Acceptance is in progress.
- Broader Orchard, v3, soak/crash/debug/disaster-recovery gates remain below and
  in the roadmap. They are not silently claimed by local unit tests.

The candidate is not release accepted while these gates are outstanding.

## Service snapshot before upgrade (2026-10-14)

The daemon on 127.0.0.1:7780 answers with Tart inventory, zero worker leases/jobs,
five image_ready records and local 0/2 VM slots used. MacBook is offline and HTPC
is asleep; cached HTPC goldens remain discoverable for wake-by-lease.
Installed daemon SHA-256 is
`2e513245263f248f36fc636c85a8f9c07f06d9610c4df011aca7e173202486a8`.
Build metadata says `821636f878311ac84570cb4cbf81bb61919c9849`, modified=true.
This is a timestamped snapshot, not a clean-release attestation.

## Tart image catalog for Broker (2026-10-12)

Catalog registration was moved outside optional Lume image_tools. The installed
Tart service returned image inventory with macos27 present/ready, and served the
persistent-token/Disconnect console controls. A graceful restart preserved the
pre-existing ready Broker lease. This establishes catalog compatibility and
non-destructive restart behavior for that candidate, not a full workload run.

## Tart image adoption (2026-10-07)

The service ran with `runtime.provider = "tart"` (Tart 2.40.1, Packer 1.15.2,
`cirruslabs/tart` plugin 1.21.0, `virfieldd` at `ae7b3df`) and its own
`state_dir`, storage on the external SSD container `samsung-tart`. Both
goldens were built and qualified by the durable image job
(`resolve → build → qualify → stop`) without manual guest steps:

| Golden | Upstream digest | Guest | Xcode | SIP | Build + qualify |
|---|---|---|---|---|---|
| `ventura-xcode-15.2` | `macos-ventura-base@sha256:9fb387bb…65ef1c5` | 13.7.4 (22H420) | 15.2 (15C500b) | disabled | 4 min + 5 min |
| `monterey-xcode-13.4.1` | `macos-monterey-vanilla@sha256:4bc904cf…391842` | 12.7.6 (21H1320) | 13.4.1 (13F100) | enabled | 17 min + 5 min |

Qualification evidence (`images/<id>/verification.json`) records the upstream
and recipe digests, OS version/build, canonical SIP state, a settled Finder
desktop, APFS encryption at rest with FileVault off and unlocked volumes, a
rotated `admin` password that persists across reboot, the Xcode signature,
version, first-launch and Swift probe, and host DeviceSupport extracted over
the guest's read-only NFS dyld export. Each golden regenerates its SSH host
keys in the build; qualification pins the new key.

Two-clone acceptance per golden (two concurrent leases, same machine
identity, distinct MACs): 23/23 checks passed for each. Each lease became
`ready` only after per-lease SSH isolation, had a host key distinct from the
other lease and from the golden, reported the expected build, SIP state and
Xcode, compiled and ran Swift, exported an artifact over pinned `scp`, and
rejected both the other lease's key and the golden's management key. Release
deleted both lease VMs and left the golden stopped.

Defects found and fixed during the live run: Tart storage probed a non-mount
point (`f491ed4`); the guest APFS volume was not grown (`6391842`, `fef184b`);
concurrent `packer init` raced (`6391842`); DeviceSupport rejected Xcode's
extractor bundle file (`ea23137`); first dial after a daemon update hit macOS
local-network session reset (`99951c7`); `nfsd restart` is refused with SIP
enabled (`ae7b3df`). Host requirements: Full Disk Access for the signed
`virfieldd` (identifier `ai.virfield.virfieldd`), and Tart
`registry_credentials` for GHCR because the host Docker credential is rejected.

### macOS 27 UI-test golden and Broker smoke (2026-10-08)

Template `macos27` (the ID Broker's macOS workers already use) now resolves to
a Tart golden built from `macos-golden-gate-xcode@sha256:324ea565…6fab88fab`:
macOS 27.0 (26A428, the same IPSW as the former Lume golden), SIP disabled,
upstream Xcode 27.0 (27A266a) adopted as `/Applications/Xcode.app`, and the
shared `uitest-27-v1` recipe (Homebrew tools, Peekaboo, TCC grants, AMFI,
automation mode). Qualification passed including the UI-test probe; the build
took 1.5 minutes and qualification 2.5 minutes.

Broker's unchanged live smoke (`scripts/macos-smoke.py` in `execution-broker`,
Balda MCP → Prism story → Broker `macos-xcode` worker → Virfield lease → XCTest)
passed on run `run-af3ed27f9f7466cca2401cc5ab44897f`: state `succeeded`,
cleanup `done`, XCTest 1 executed / 0 failed, xcresult present, six hashed
artifacts. No Broker code or configuration changed.

The first smoke attempt found two more defects, both fixed: a detached
`tart run` that exited at once (VM still held after Packer) was reported as a
start (`b0e5bf7`), and the Tart inventory ran `tart get` sequentially inside a
5-second budget, which ASIF disks (about one second each) exceeded, pausing
admission (`2101918`).

Not yet covered: VS Code remote-debug against Tart leases, GHCR publication of
the goldens, Orchard, and restart during a Tart build.

## Remote nodes R1 (2026-10-08)

Two physical hosts joined the Tart service as `baremetal` nodes over pinned SSH
([design](superpowers/specs/2026-10-08-remote-nodes-r1-design.md)):
`mbp-2014` (MacBookPro11,1, Monterey 12.6.8 booted, `admin`, no sudo) and `htpc`
(Bazzite 43, `bazzite`, no sudo, Wake-on-LAN, sleep-guard file
`/tmp/rsync-active`). Inventory reported each booted environment as a
pseudo-golden: `mbp2014-monterey` and `htpc-bazzite` available, the other
MacBook volumes `artifact_missing` because they were not booted.

Live acceptance (20/20): both node leases and one Tart VM lease were admitted
concurrently and the Mac reported 1/2 VM slots used. Each node lease reached
`ready` through node-side SSH isolation, accepted its own key with the pinned
host key, had its workspace and keep-awake (`caffeinate`, the guard file), and
rejected the other node lease's key. Release revoked both keys, removed the
workspaces and tagged `authorized_keys` lines, and released keep-awake.

Defects found by the live run and fixed: node leases were charged to Mac
resource admission (`754f5d2`) and then blocked every Mac VM admission
(`7746a8f`); a sleeping node stretched inventory refreshes past the 10-second
staleness limit (`23bf2e2`); inventory probing every 2 seconds would have kept
the HTPC awake through its SSH-aware sleep guard (`0a6568b`, now once a
minute). Under heavy host load (Finder at 356% CPU crawling the VM SSD),
Tart's disk-sizing commands took 12–18 s and admission paused briefly; this is
fail-safe but noisy.

Big Sur (2026-10-08): with the MacBook booted from its Big Sur volume
(11.7.9), the node presented a second host key and the `Big-Sur`
pseudo-golden. After enrolling the management key on that volume and adding
the key to `host_keys`, a `mbp2014-big-sur` lease passed 5/5: the Monterey
template was refused as `template_unavailable`, the lease reported the Big Sur
host key, lease SSH and `caffeinate` worked, and release revoked the key and
the keep-awake.

Catalina (2026-10-08): the same enrollment on the Catalina volume (10.15.7;
host key fingerprint confirmed at the MacBook's own console) and the same
`mbp2014-catalina` lease test passed 5/5. On Catalina the Sharing pane hung on
"Remote Login starting…"; `sudo launchctl load -w
/System/Library/LaunchDaemons/ssh.plist` started SSH instead.

Ventura (2026-10-08): enrolled the same way (13.7.8; host key trusted on
first use at the operator's request, ED25519
`SHA256:KtTqiHnjWkqG64oxDwJzP0XPiND85QPshoa1rBgMqeg`); the `mbp2014-ventura`
lease test passed 5/5. All four MacBook volumes are now leasable, one at a
time, as whichever is booted.

Wake by lease (2026-10-08): with the HTPC suspended, an `htpc-bazzite` lease
was admitted while inventory reported the node `asleep`; the clone sent
Wake-on-LAN and the lease was ready 27 s later with lease SSH and the
keep-awake file in place, then released cleanly. The HTPC's own sleep guard
(`~/htpc-sleep-guard.py`) never let the node sleep again after a long
Virfield hold, because the desktop idle timer fires once and had already
elapsed under the hold; the guard now suspends 30 minutes after it releases a
hold unless something holds sleep again or another block inhibitor (game,
video) is present.

macOS VM leases on the HTPC are recorded under Remote nodes R2 and desktops
under R3.

## Remote nodes R2: macOS VMs on the HTPC (2026-10-08)

Bootstrap goldens Sonoma 14.8.9 (23J631) and Sequoia 15.8.1 (24H32) were
installed by hand under `dockurr/macos` on the HTPC and stored read-only on its
HDD ([spec](superpowers/specs/2026-10-08-hackintosh-nodes-r2-design.md)).
Virfield then secured both as `htpc-sonoma-14` and `htpc-sequoia-15`: clone,
password rotation, management key, key-only SSH, auto-login, reboot, and
verification of version, SIP disabled, desktop and password, then commit
(Sonoma 13 minutes, Sequoia 21 minutes, commits about six minutes each on the
HDD).

Live acceptance `accept-r2.py` passed 19/19: `htpc-sonoma-14`,
`htpc-sequoia-15` and the `htpc-bazzite` host lease admitted together (HTPC
3/3); the VM leases served SSH on 192.168.1.86:22100 and :22101 as `admin`
with the lease key and pinned host key, reported 14.8.9 / 15.8.1,
`iMacPro1,1` and SIP disabled, and refused the other lease's key and the
bootstrap password; the host lease saw both VM containers; release closed both
ports and left no lease folders, containers or keep-awake on the node. Both
VMs became ready 550 s after acquisition while booting together off the HDD.

Defects found by the live runs and fixed: a dropped SSH handshake through the
port forward was fatal and the probe reported SSH before the guest listened
(`0b0cf05`); lease isolation timed out after one minute while `sysadminctl`
was still running in a slow guest (`dcf96e2`); right after an image commit a
stale probe sent a lease down the host-lease path (`163caa4`).

## R3 desktop access (2026-10-08)

Live acceptance `accept-r3.py` with a Tart `ventura-xcode-15.2` lease and an
HTPC `htpc-sonoma-14` lease passed 11/11: each desktop tunnel returned a
`vnc://admin:…@127.0.0.1:<port>` URL whose port matched its loopback address,
answered with Apple RFB `RFB 003.889`, and carried the lease account's password
(`dscl -authonly` over lease SSH); each lease's SSH tunnel was separate; after
release both desktop tunnels refused connections and the HTPC kept no clone or
container. Opening the Tart URL in Screen Sharing.app signed in without a
prompt and showed the Ventura desktop.


## Not yet accepted

- Tart VS Code debug, GHCR publish/pull-back, restart during build and a full
  lifecycle crash matrix; prolonged soak and VM-disk disaster recovery.
- Orchard lifecycle, guest credential isolation, forwarding, worker restart/drain.
- Current candidate workflows in both Codex and Claude Code; full apple-browsers
  UI-test workflow through Broker. The accepted Broker smoke is narrower.
- Remote/container TLS and routing through the intended production entrypoint.
- Fresh clean release archive extraction, native URL-scheme launch, complete
  backup/restore and upgrade/rollback on the current provider configuration.

## Release procedure

A new release requires a clean committed source revision; all CI/release checks
must pass for that exact revision. Build with `tools/release.py NEW_VERSION
--output NEW_DIRECTORY`; it packages committed source, fresh macOS/arm64 binaries,
the native browser, dependency notices, hashes and a revision manifest. Existing
outputs/tags must not be overwritten. Validate extraction, signatures, URL scheme,
installation, live workflows and cleanup. Publishing/VM deletion require their
own authorization. The only published archive is historical v2.0.0 (2026-09-30).
