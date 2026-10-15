# Verification

Current record, reconciled 2026-10-15. Accepted deployment: local Tart plus SSH
nodes and Podman macOS lab VMs. Virfield switched to Tart on 2026-10-08. On 2026-10-14 the retired separate
Lume LaunchDaemon was disabled and booted out after verifying empty inventory; its rollback files are preserved and port 7777 is closed.
[Earlier Lume acceptance and release records](history/VERIFICATION-before-2026-10-14.md)
are historical evidence, not current gates. Later passing tests do not invalidate
newly reproduced defects below. Source, installed candidate and published release
are distinct artifacts.

## Acceptance checkpoint (2026-10-15)

Current deployment is `03b0d23` / `0.0.0-native-login-auth`, installed in a
verified idle pool with private backup, the previous signing/FDA identity,
configuration, keys and all seven baseline image manifests preserved. Exact
installed HTTP/fresh stdio version, all four runbooks/resources, discovery, status
and anonymous 401 passed in 1.26 seconds. Full local CI (seventeen Python tests,
Go vet and the complete race suite), pinned Staticcheck 0.7.0 and Gitleaks (227
commits) passed for that commit. The actual owning MCP transport closed after its
old process was terminated and did not reconnect automatically. The user confirmed
manual Virfield MCP Restart, but the subsequent owning-client help call still
returned `Transport closed`. Fresh stdio is not current-client acceptance.

Fresh native `03b0d23` acceptance failed: both Sonoma fixtures and one Samsung
Monterey failed in GUI `login-rekey`. All three were released and SSH revocation
passed; the Sequoia pair was not dispatched. Earlier `f8b64f5` phase logs established
failed readiness after a five-minute installer wait, but did not prove that its
CLI or native caller hung. A later live Monterey trace caught the owned Aqua
agent already exited with code 65 almost immediately. The installer kept waiting
for a result that would never appear. Earlier Sonoma preflight failure's exact
backend cause remains unknown. Its later Finder screenshot is not proof of that
cause. A separate trace disproved the stale-Quartz-context hypothesis: the SSH
caller's Quartz session followed the real root/waiting to admin/desktop login.

Native unlock rejected all known candidates on the original login keychain,
while the same production caller passed on an isolated keychain inside Monterey.
Guest identity credentials matched controller/image credentials; five retained
image-credential backups contained the same source secret. The original reported
unlocked (flags 7) yet returned `-25293` for authentication and SecItem add. Its
private on-disk copy authenticated with the current password. Direct password
change on the original also failed. One native lock/unlock with that already
validated password completed in 58 milliseconds and restored authentication plus
native add/read/delete. This establishes inconsistent cached keychain state and
a working refresh, without establishing every internal macOS mechanism or all
historical drift causes. No original items were deleted. Diagnostic-only import
and Ref failures were collector errors, not product failures.

The next source correction validates a failing password on a temporary owner-only
on-disk copy before allowing one original-cache refresh. Wrong passwords never
lock the original; failed refresh does not select another password. Native tests
cover locked/unlocked/public-base fixtures and injected stale-cache responses
with real copy authentication, refresh, cleanup and existing-item preservation.
The agent records native substages/all attempts and propagates an exact
scope/boot/mode/nonce exit receipt promptly; stale failures cannot abort a new
request. These changes still require a clean package and fresh installed matrix.
A staged second account rotation on the diagnostic Monterey preserved a fixture
item and passed full native Aqua I/O, unlocked console, no visible assistants or
SecurityAgent, and 1920×1080. That run did not reproduce the cache fault and is not
clean installed acceptance. Its account password was changed again, deliberately
leaving its controller credential stale until exact-fixture cleanup. That fixture
was released with its original journaled identity and release key. Do not use
that diagnostic for acceptance or replay its account mutations. The subsequent
13:52 UTC status contained no worker leases or jobs and all seven baseline images.
The correction passed the full local CI and pinned Staticcheck 0.7.0; five native
authentication cases passed, including the two injected stale-cache cases.

Worker preflight retains bounded private `leases/ID/desktop.log` with deadline,
command budget, duration, partial observations and original error even before
worker credentials exist; it does not overwrite the golden's diagnostics.
Private evidence under `/private/tmp`: `virfield-installed-htpc-native-f8b64f5-coexist-20261015`,
`virfield-installed-samsung-monterey-f8b64f5-20261015`,
`virfield-installed-htpc-native-03b0d23-coexist-20261015`,
`virfield-installed-samsung-monterey-03b0d23-20261015`, and
`virfield-monterey-auth-channel-03b0d23-20261015`.

Full release acceptance remains open. The following paragraphs are dated
checkpoints superseded by this current record.

Deployment snapshot at 09:47 UTC: `558a6cb` / `0.0.0-cold-reboot-checkpoint`
is installed with its original signing/FDA identity, private backup and all seven
image manifests preserved. The unrelated MacBook lease was released by its owner
before the idle installation; no foreign lease was changed. Exact HTTP and fresh
stdio version, all four runbooks/resources, discovery, status and anonymous 401
passed in 1.29 seconds on a repeat. An earlier HTTP runbook request timed out at
ten seconds and inventory repeatedly became stale. Neither empty HTPC acceptance
journal dispatched a mutation: both stopped at the healthy-idle guard.

The controller Mac was under extreme unrelated load (observed one-minute load
up to 455 and about 38 GiB swap in use). A stale-inventory sample and process
inspection caught the daemon waiting for a `diskutil` volume lookup. The old
runtime starts that lookup for each container on every refresh. This identifies
a costly refresh dependency; it does not establish every timeout's cause. Source
now caches only the UUID against the kernel fsid, device and mount point, checks
the directory and fresh allocatable bytes with `statfs` on every call, and checks
the mount again after a cold lookup. Missing storage, replacement mounts, failed
lookups, lookup races and cancellation fail closed in regression tests. Real
read-only probes of both configured storage directories returned the exact pinned
UUIDs: twenty subsequent fresh capacity reads took 0.12–0.22 milliseconds per
directory with only one `diskutil` call. This new correction still needs its own
clean package, installation and full native provider repeat. Evidence:
`/private/tmp/virfield-inventory-watch-558a6cb-20261015` and
`/private/tmp/virfield-volume-live-20261015.log`.

The ten-minute post-reboot verification correction is installed; a full current
HTPC matrix is not yet accepted. The owning MCP client last served `2724f94`
runbooks, and native browser GUI acceptance and existing HTPC golden permission
migration still await their separate human gates. A later runbook-only package
must keep dated code-test provenance distinct from its exact `release.json`.

At 09:49 UTC the clean UUID-probe correction was installed as `29e2bc9` /
`0.0.0-volume-refresh`, preserving seven manifests and the signing identity.
Full local CI, pinned Staticcheck and Gitleaks (220 commits) passed; govulncheck
reported zero reachable/imported-package vulnerabilities. Exact archive stdio
passed. Installed HTTP/fresh stdio passed on a repeat in 22.55 seconds, after an
earlier ten-second HTTP timeout. Variable API delay remained: a second daemon
sample showed SQLite `fcntl`/read activity, with no other process owning the DB.
The actual root-owned launchd job uses `Background`, with thread priority `4T`.
Reversible changes to this daemon's background/latency/throughput policies did not
establish complete causality or replace the registered plist. The service template
now uses `Standard`; persistent migration of this old job remains pending and
needs an idle administrator registration. No foreign build was stopped.

The first acquire's unknown reply was inspected against the journal and empty
server inventory before retrying its exact key/body/SSH identity. Its next reply
was a typed `backend_unavailable` rejection before reservation. A subsequent
healthy, responsive interval admitted the same request and its second Sonoma.
At that checkpoint both owned leases were provisioning; the full provider matrix remained pending.
The original post-install diagnostic collector indexed the optional worker
`purpose` field and produced collector errors once leases appeared. Those rows
are not daemon outages; the corrected collector records a separate series.
Private evidence: `/private/tmp/virfield-installed-htpc-native-29e2bc9-20261015`
and `/private/tmp/virfield-inventory-watch-29e2bc9-r2-20261015`.

The subsequent `29e2bc9` Sonoma pair failed. The first received
`lease_ssh_failed` during credential isolation before native keychain acceptance;
its private provision log was empty, while later pinned SSH/RFB diagnostics
worked. A deterministic SSH regression shows why timeout logs could be empty:
`RunReader` discarded bounded stdout/stderr when the context expired. Source now
preserves those bytes privately and records a fixed failure class and timestamped
credential phases without values. The exact first guest command that failed is
not established; no mutation timeout was increased on this evidence.

The second Sonoma became ready at `10:07:17 UTC`, then entered `vm_drift` at
`10:09:25 UTC`. Fresh native Aqua/new SSH I/O subsequently passed without manual
GUI unlock; the API state was not repaired. The actual drift observation was not
retained, and a later inventory captured during cleanup cannot identify its cause.
A deterministic regression nevertheless reproduced a concrete false-drift bug:
secondary-runtime observation can succeed with an unavailable host and cached VM
without an address; repeated ticks treated that unverified state as a permanent
identity change. A running VM with an unknown address also incorrectly drifted.
The new controller preserves the verified lease in both cases, resets the drift
counter and blocks new tunnels with retryable `backend_unavailable`. Recovery
needs no replacement; verified missing/stopped VMs and changed known IP/port still
trigger drift. This does not retrospectively prove the cause of the older iPad
Monterey event or the exact second Sonoma observation.

Both exact own fixtures were released and tunnel revocation passed. The failed
pair's cleanup also exposed a harness bug: releasing the still-running second
preparer received typed `409 operation_in_progress`. Owned cleanup now waits
boundedly only for that pre-dispatch rejection using its original key/body.
Lost replies, foreign IDs and other conflicts are not automatically replayed.
Full local CI (seventeen Python tests, Go vet and the race suite) and pinned
Staticcheck passed for these source corrections; a new installed native repeat
remains required.
Evidence: the journal above, `virfield-a0bc-vm-drift-20261015.json`, and the private
`virfield-drift-red/green-20261015.log` and `virfield-credential-progress-red-20261015.log`.

Installed `750a896` / `0.0.0-observation-recovery` passed exact HTTP/fresh stdio
MCP in 1.24 seconds and two complete sequential Samsung Monterey 12.7.6 /
Xcode 13.4.1 cycles: native Aqua/new SSH I/O before/after real reboot, fresh
scope/boot/nonce, 1920×1080, cross-key rejection, Swift compile/run, workload,
artifact, RFB, release and tunnel revocation. The separate iPad template's new
repeat is storage-blocked: its internal container had about 100 GB available,
below the approximately 150 GB full-growth-plus-reserve admission requirement.
No reserve was lowered and no cross-container workaround or foreign cleanup was
attempted. Earlier accepted iPad receipts retain their dated scope. A new
concurrent HTPC matrix on `750a896` subsequently exposed the startup and Sequoia
UI/authentication defects below. Private journals:
`/private/tmp/virfield-installed-samsung-monterey-750a896-20261015` and
`/private/tmp/virfield-installed-htpc-native-750a896-20261015`.

A further real-TCP regression found that preserving the lease alone still closed
an established stream when its address became unverified. Source now preserves
existing streams, pauses new clients on cached listeners, checks endpoint
freshness before and after a new upstream dial, and resumes the same listener
after recovery. The test covers unknown address, whole-fleet failure, freshness
expiry without another tick, recovery and final lease expiry. Observed stop or
changed known endpoint still revokes connections. Full local CI (seventeen Python tests, Go vet/race) and pinned Staticcheck
passed. This source-only correction awaits an idle installation; it is not
included in `750a896`.
Evidence: `virfield-tunnel-continuity-red/green-20261015.log`.

The first Sonoma's new reboot exposed a concrete startup cause rather than a
long native I/O operation. Pinned read-only diagnostics showed a new boot UUID
and a running Finder, but startup/result still belonged to the old boot. At
`10:43:39 UTC`, `launchctl print gui/501/ai.virfield.guest-keychain` reported
`state = not running`, `runs = 0`, never exited, and
`pended nondemand spawn = speculative`. At `10:45:23 UTC`, one journaled
`kickstart` without `-k` of that exact owned agent changed it to runs 1. Fresh
native Aqua/new SSH proof and the real-reboot workflow then passed without manual
GUI password entry. Both Sonoma fixtures completed their workloads/artifacts/RFB
and were released. This is a demand-start experiment, not an unmodified `750a896`
automatic-start PASS. The live helper also reads the repository's verification
script at each probe, so later probes can include this source correction while
the controller remains `750a896`; `acceptance-source-mix.json` records that scope.
The Sequoia pair continues separately.

Source now demands the existing agent during initial qualification and before
waiting for post-reboot startup. Worker SSH waits for registration and current
scope/boot proof before the original command, then unlocks its own audit session;
image bootstrap SSH remains usable. A completed automatic run is reused only in
the same scope, boot and Aqua audit session, preventing a duplicate demand race
without skipping a new GUI login. Explicit native requests are never skipped.
The SSH wait is deducted from the original verification budget; no timeout was
increased. Real shell regressions cover deferred registration, one demand without
killing a running agent, missing/failed startup, preserved stdin, image bootstrap,
new audit sessions and exhausted inherited budgets. Targeted tests, full local
CI (seventeen Python tests and Go vet/race) and pinned Staticcheck passed. A new
installed worker-bridge repeat remains required. Private
evidence: `first-sonoma-agent-registration.log`,
`first-sonoma-agent-demand-start.log`, `first-sonoma-agent-demand-intervention.json`
under the HTPC journal above.

The first Sequoia reboot reproduced a separate authentication/desktop coupling
bug. At `11:05:45 UTC` its GUI agent failed in `login-native-io`. The retained
native receipt had all three SecItem statuses zero, an unlocked keychain, matching
data and an Aqua session, but Keyboard Setup Assistant made overall `ready`
false. The old password loop treated that desktop failure as a bad password,
locked the working login keychain again and tried the public fallback. The
framebuffer then showed both Keyboard Setup Assistant and a CommCenter request
for the login-keychain password. The actual HID inventory contained both QEMU
USB (`1-1575-0`) and Apple PS/2 (`65535-1452-0`); only the first had a learned ANSI
mapping. Adding the second mapping and demanding the owned agent recovered
native I/O and removed the keyboard assistant, but the existing CommCenter
password dialog remained. Mapping absence alone is not established as the cause.

A separate disposable empty-keychain experiment verified that password change
rejects an incorrect old password even while unlocked, preserves an existing
item, and makes the new secret usable after locking that fixture. The real login
keychain therefore need not be deliberately locked to authenticate a candidate.
The source correction uses that API, separates native authentication from UI
readiness, and never tries another candidate after an authenticated rekey's I/O
failure. Real-shell regressions cover current/previous/public-base migration,
blocked desktop, unknown passwords and failed native I/O, with zero login locks.
Native desktop verification now rejects visible SecurityAgent windows, including
the observed layer-1000 password dialog, and fails if the window snapshot is
unavailable. No window titles or keychain contents are collected.

Staging this correction on the exact retained Sequoia with unchanged account
credentials returned `keychain_ready:true`, all SecItem statuses zero, and
`security_dialog_visible:true, ready:false`, correctly retaining the old modal
as a failure. Its distinct regression reboot then passed fresh native Aqua/new
SSH I/O with no Keyboard Setup Assistant or SecurityAgent windows. However,
framebuffer acceptance showed the macOS lock screen: that receipt is not a full
desktop PASS. The old original reboot was not replayed. Read-only loginwindow
history established screensaver idle lock at `11:26:33 UTC`, which persisted
`autoLoginUserScreenLocked=1`; the next boot restored it through
`kLWLockFromCookieFile` at `11:42:35 UTC`. Both system and display sleep were
already disabled. This independently demonstrates why Finder/Aqua/I/O alone
cannot certify a usable desktop.

The additional correction disables general/per-host screensaver idle time,
clears and verifies the autologin lock flag when preparing account credentials,
and checks the native Quartz console/login/lock state in both the read-only
desktop preflight and full Aqua readiness. The exact two owned Sequoia fixtures
correctly returned locked-console failures after staging this check. Their
first unshipped console implementation incorrectly required an explicit false
lock flag. Quartz omits that key on an unlocked console; a read-only native
controller probe confirmed the omission. The corrected predicate accepts absent
or false only with a valid on-console, completed login; true, malformed values,
missing session, switched-out console and incomplete login remain failures.
Actual JXA regressions run both production predicates against these cases.
The strict-check reboot was not replayed: its exact first fixture was released
with its original durable cleanup key and SSH revocation passed. The old owner
harness was terminated without resuming its cleanup logic. The second fixture's
corrected reboot reached an unlocked desktop, but its framebuffer showed a new
Spotlight password dialog explicitly naming the `virfield-lease-...` scoped
keychain. A persistent custom default is locked before the owned Aqua agent
runs; its later unlock does not dismiss the queued dialog. Full readiness
correctly remained closed. Source now keeps the preserved/rekeyed login keychain
as default and first search entry across boots; the scoped keychain remains
supplemental, private and unlocked. A real-shell regression checks default/search
order and missing-login failure. This final policy requires fresh installed clones
and real reboot acceptance. The second exact fixture was also released and SSH
revocation passed. Neither failure was answered by a GUI password or relabelled
a desktop PASS.
Full local CI (seventeen Python tests and Go vet/race), targeted actual-shell/JXA
regressions and pinned Staticcheck passed for the final login-default source.
This is guest-source staging, not installation of a new controller package.
Evidence lives under
`/private/tmp/virfield-installed-htpc-native-750a896-20261015`, especially
`login-auth-regression-stage.json`, `login-auth-regression-reboot`,
`console-policy-regression-r2`, and the dated Sequoia framebuffer images. Lock
origin logs are retained as `virfield-sequoia-lock-origin-allboots-20261015.log`.

An earlier code checkpoint installed `8c521fe`, version
`0.0.0-desktop-preflight`. Subsequent runbook/acceptance-tool packages retain this
dated code-test provenance and carry their own exact `release.json` revision.
Factory capacity, node temporary-file permission, stable-TSC
capability, console/GUI observations and virtual keyboard corrections are installed. Exact-source local
`make check` and Staticcheck passed; security scan evidence is recorded below.
HTTP and fresh stdio MCP passed
exact-version discovery, all four runbooks, resources and authenticated status;
anonymous MCP was rejected. These checks do not attest already-connected clients.
The `8e07015` checkpoint introduced native GUI/SSH keychain readiness, explicit
`1920x1080px` Tart displays, interrupted-inventory protection and the separate
private HTPC HDD root. Its native Apple browser accepts a separate private
state directory; compiled Swift trust checks passed against the actual deployment
and reject exposed files, symlinks and foreign origins. The complete local
`make check` (10 Python tests and Go race suite) and Staticcheck passed.

Eight disposable Tart clones passed on installed `4a53b67`: two each of the new
iPad Monterey, original Monterey, Ventura and macOS 27 profiles. Each passed
native WhenUnlocked add/read/data comparison/delete in Aqua and a new SSH session
before/after a real reboot, fresh boot UUID/nonce, actual GUI 1920x1080,
OS/build/SIP, cross-key rejection, distinct host keys, artifacts, RFB desktop,
release and tunnel revocation. All eight VMs were removed; seven goldens remain.
The VM implementation and guest scripts are unchanged in `8e07015`.
Private journals: `/private/tmp/virfield-installed-monterey-pixel-4a53b67-20261015`
and `/private/tmp/virfield-installed-tart-native-4a53b67-20261015`.

Installed `8e07015` HTTP and fresh stdio exact-version/all-four-runbooks/resources
smoke passed; the owning client at that earlier checkpoint still served old Lume docs.
Native browser URL-scheme registration passed; GUI launch acceptance is blocked
by the locked Mac and is not claimed.

The first concurrent HTPC run failed with `vm_missing` immediately after confirmed
clone. Both owned fixtures were released and their native folders/containers were
independently confirmed absent. A deterministic test reproduced node inventory
invalidation being consumed while a mutation was running. The new source
invalidates again on completion, including partial failures; Control waits for a
snapshot started after clone completion. Regression tests, full local CI and
Staticcheck passed. The subsequent installations and repeats are recorded below.
Evidence: `/private/tmp/virfield-installed-htpc-native-8e07015-20261015`.

The correction was subsequently installed as `2f46faa`; exact-version HTTP/fresh
stdio MCP passed. Two concurrent HTPC clones passed clone/start with durable
8 CPU / 16 GiB RAM / 200 GiB disk reservations on private HDD workspaces.
They did not reach guest SSH before the ten-minute operation deadline: the
framebuffer showed macOS boot progress, while host I/O pressure was high and the
container used direct I/O (`cache=none`). Both owned fixtures were released.
The new candidate uses QEMU's standard flush-preserving `writeback` cache and
threaded I/O; its effect on native boot/readiness still needs live verification.
Evidence: `/private/tmp/virfield-installed-htpc-native-2f46faa-20261015`.
Installed `ab9a2ad` repeated the two-VM boot with flush-preserving page cache,
but it also hit the deadline. Read-only process counters and descriptors identified
Steam staging a 33.6 GB update on the same HDD, about 42 MB read and written per
two seconds, with high host I/O pressure. This external workload was preserved;
the page-cache change's performance effect is not yet established. All owned
failed fixtures were released and native absence confirmed. Node native acceptance
must be repeated after storage contention clears.
The source budgets 20 minutes for node VM preparation because the earlier cold
two-VM HDD boot already took nine minutes before native keychain verification.
The primary VM budget and caller lease TTL are unchanged; all readiness checks
remain mandatory. The budget correction is installed. The next repeat reached
SSH after Steam completed its update but exposed a shorter GUI readiness deadline.

On installed `2724f94`, one concurrent Sonoma clone timed out waiting 45 seconds
for GUI native I/O; the second became ready. After its real reboot, verification
failed at `05:18:16.696 UTC`, but the automatic agent wrote
valid native GUI proof and this boot's startup marker at `05:18:18 UTC`, 76 seconds
after boot. A new GUI/SSH native probe subsequently passed with the new boot UUID.
The exact rejecting preflight guard was not retained in that run. The observation
proves that the guest became natively ready after a failed check; it does not prove
a permanently locked keychain or identify the shorter console versus startup gate.
The first clone's incomplete native probe also has less specific evidence.
Installed `edcb756` allowed 120 seconds each for the automatic startup and requested
GUI result, within the five-minute command and existing preparation context.
Both concurrent Sonoma clones then became ready, but a post-reboot check still
failed before native proof. Source also corrects the separate 30-second console
login guard: a deterministic execution of the real shell block rejected a valid
delayed login, then passed after allowing 120 seconds; an absent login still fails
within its bound. Console/startup errors are now distinct. Native I/O,
scope/nonce/boot matching and fail-closed readiness remain unchanged.
Private phase timestamps improve diagnostics without recording secrets. Evidence:
`/private/tmp/virfield-installed-htpc-native-2724f94-20261015/journal.json`.

A subsequent retained Sonoma clone stalled after the guest reboot command:
no fresh SSH/boot UUID, a stale pre-reboot desktop frame, active guest CPUs and
block I/O idle for minutes. The own runner was stopped before cleanup to retain
diagnostics, then both exact owned fixtures were released through their journal.
No additional reboot or reset was dispatched. The pinned container's CPU gate
incorrectly tests VMX TSC scaling before exposing invariant TSC. HTPC has
`constant_tsc`, `nonstop_tsc`, active Linux clocksource `tsc`, and no VMX scaling;
the actual QEMU command omitted `invtsc`. This is the upstream capability-detection
defect corrected in [qemus/qemu #1513](https://github.com/qemus/qemu/pull/1513).
Source now supplies `+invtsc` only when every CPU has both stability flags and
Linux uses TSC, otherwise explicitly `-invtsc`. Missing/heterogeneous flags and
non-TSC clock fixtures fail closed. The next installed candidate's two real
Sonoma reboot cycles subsequently passed; this is evidence of improvement, not
proof that every earlier stall had this single cause. Retained evidence:
`/private/tmp/virfield-htpc-console-diagnostic-edcb756-20261015`.

The retained desktop also exposed Keyboard Setup Assistant over the UI. The
QEMU USB keyboard (vendor 1575/product 1/country 0) had no learned layout, while
the Apple virtual keyboard entry was present. Source registers only this fixed
virtual ANSI device, preserves other mappings, closes the own VM's assistant,
and rejects GUI readiness while it is running. Actual `defaults` output was
checked to contain an integer layout value.

Installed `7e9c5ad` passed native Aqua/new SSH I/O before/after real reboot on two
concurrent fresh Sonoma clones, with distinct boot UUIDs/nonces, actual 1920×1080
and no Keyboard Setup Assistant. CPU `+invtsc` was verified on both real QEMU
command lines; simultaneous 8 CPU / 16 GiB RAM / 200 GiB disk reservations and
private HDD placement passed. The first complete workload/artifact/RFB cycle
passed; the second native/workload/artifact cycle reached desktop tunnel creation
and received HTTP 503. Both own clones were released and tunnel revocation passed.
The complete provider matrix is therefore not accepted yet. The exact 503 payload
was not retained, so its code is not claimed. Evidence:
`/private/tmp/virfield-installed-htpc-native-7e9c5ad-20261015`.

The runner now records typed HTTP rejections and waits boundedly only for a known
retryable `backend_unavailable` on lease/service-keyed tunnel opening. That server
rejection occurs before listener allocation. Nonretryable errors, VM mutations
and lost POST replies are not automatically replayed. Regression fixtures cover
recovery with an identical request, refusal without replay and private journals.
A separate deterministic regression found the outer three-minute SSH-preparation
cap truncating the bounded five-minute guest GUI command. Source now uses the
existing persisted job deadline for the whole sequence, retaining per-command
caps, original overall preparation budgets and caller TTL. These orchestration
changes still need a new installed workflow acceptance.
They were subsequently installed as `0838018`; exact-version HTTP/fresh stdio MCP
passed. Full local CI includes twelve Python tests and the Go race suite.

Further deterministic real-SSH tests reproduced cancellation being ignored while
the node peer withheld command-session or forwarding-channel acknowledgement.
The waits occurred before the existing cancellation select. Source now closes the
owning transport during channel creation cancellation and during a stalled
handshake; it detaches cancellation before returning a completed forwarding
connection. Red/green channel tests, canceled-handshake coverage and continued
traffic after canceling a successful dial's creation context pass. This bounds
node commands/probes without shortening an established tunnel's lifetime. It is
not proof of the earlier payload-less HTTP503's precise cause. This correction
was subsequently packaged and installed as `c85a899`; provider workflow
acceptance remains open.

The forwarded guest transport exposed a related reproduced defect:
`x/crypto/ssh` channels reject socket deadlines, so ignoring `SetDeadline` left
the guest command timeout ineffective there. Guest handshake and command limits
now also use cancelable contexts, including channel creation; the dedicated node
transport closes before its child channel so shutdown cannot wait on that peer.
The command fixture without socket-deadline support failed on the old
implementation; both command and banner timeout fixtures now pass with these bounds. Host-key and authentication
rejections still fail without fallback; the wider guest/node race suites pass.

Installed `c85a899` passed exact-source full local CI (twelve Python tests,
Go vet/race), Staticcheck, Gitleaks (215 commits) and HTTP/fresh stdio MCP
exact-version/all-four-runbooks/resources/status. Its next concurrent node run
failed before keychain agent installation: one Sonoma console login did not
become ready within 120 seconds. Both owned failed fixtures were released.
The cause is not established by that error alone. Two additional owned Sonoma
fixtures then passed native Aqua/new SSH I/O before/after real reboot, fresh boot
UUID/nonce, 1920x1080, exact OS/build/SIP, distinct host keys, cross-key refusal,
workloads, artifacts and RFB. Their starts were staggered; this does not replace
the repeat of simultaneous cold boot. The second fixture's inherited console was
already `admin` with Finder running before credential rotation, so that run did
not reproduce an autologin race. No manual GUI unlock was used. Evidence:
`/private/tmp/virfield-installed-htpc-native-c85a899-20261015` and
`/private/tmp/virfield-console-retained-c85a899-20261015`.

Inspection also found worker preparation rotating account/autologin credentials
immediately after SSH availability, unlike image qualification's prior desktop
barrier. Source now requires the same settled Finder desktop before any worker
credential mutation or secured lease identity creation. A real-SSH regression
with an absent desktop fails on the old source and passes with the barrier;
image credentials remain byte-identical. This closes the missing ordering
precondition, without claiming it caused the earlier console-login failure.
The correction was installed as `8c521fe`; its later HTPC repeats are recorded
below. Two new sequential iPad Monterey clones passed on this
installed baseline: native GUI/new SSH keychain I/O before/after real reboot,
fresh boot UUID/nonce, actual 1920x1080, exact OS/build/SIP, distinct host keys,
cross-key refusal, workload/artifact/RFB, release and tunnel revocation.
The second also reported Xcode 13.4.1 / 13F100 and Swift 5.6.1 and compiled and
ran a Swift program. This is macOS 12.7.6 / 21H1320; Xcode's 13.4.1 is not the
macOS version. Neither new clone reproduced the earlier iPad lease drift;
that does not identify the old event's missing runtime error. Evidence:
`/private/tmp/virfield-installed-ipad-monterey-8c521fe-20261015/journal.json`
and `monterey-toolchain-proof.json` alongside it.

Both simultaneous Sonoma clones on `8c521fe` passed native GUI/new SSH I/O
before/after real reboot and the resource reservation checks. The first complete
workflow passed; the second connected to the desktop listener but timed out on
its ten-second RFB greeting read. Both fixtures were released and revoked;
Sequoia was not started by that failed runner. The exact reason for that delay
is not established. The acceptance reader separately assumed a whole greeting
in one TCP read and used the same ten seconds as Control's upstream dial budget.
It now reads all twelve bytes within one thirty-second total deadline, validates
the RFB greeting and records elapsed time/failure type. Real TCP tests cover
delayed fragmentation, a partial greeting not extending the deadline, EOF and
invalid greetings. Production forwarding/guest code is unchanged by this runner
correction. The next Sequoia and Sonoma runs preserve direct node RFB, framebuffer
and console evidence before cleanup on a repeated failure.

The first Sequoia pair reached Aqua but both native agents outlasted the
120-second result guard at `login-native-io`; both failed fixtures were released.
Two subsequent retained fixtures established actual false negatives without a
manual GUI unlock: Sonoma failed at `08:02:35 UTC`, then its automatic agent
published native ready at `08:02:36`; Sequoia failed at `08:06:42`, then published
ready at `08:08:33`. Fresh GUI and new-SSH native probes passed afterward on both.
Their API leases remained `needs_attention`; no state was rewritten or adopted.
Candidate `6779b6f` shared the remaining original five-minute SSH-command budget between
startup/result observations, reserving fifteen seconds for final diagnostics.
A deterministic execution of the real shell loop fails on the old 120-second
guard and passes for valid proof at 130 seconds, while missing proof and a prior
stage spending the budget still fail within the one total bound. Native I/O,
scope/boot/nonce and the overall operation/lease deadlines are unchanged.

The retained Sonoma's direct node RFB greeting took 13.5 seconds, reproducing a
valid service slower than the old ten-second reader. Sequoia's direct node read
timed out at thirty seconds while cold. Later its guest localhost greeting through
pinned SSH took 0.3 seconds; firewall was disabled and launchd owned the listener.
After that, the node forwarding greeting took 0.01 seconds. This establishes a
readiness gap: `LISTEN` alone did not attest a responsive Screen Sharing service;
it does not identify every cause of the earlier cold-start delay. Source now
requires a real, complete RFB greeting through authenticated guest SSH before
worker readiness, within a separate ninety-second probe and the original job
deadline. Real SSH tests cover fragmented greetings, stalled banner/channel
acknowledgements, invalid protocols and preserving the provisioning connection
after success. This source correction still needs installed provider acceptance.
The worker Aqua agent also probes the actual greeting before publishing result
and startup on each login/reboot, so the post-reboot path does not rely on the
initial preparer's warm service. It skips this service for image scopes. The
actual Bash block passes TCP fixtures for fragmentation, silent/invalid services,
scope/boot binding and no image-scope dependency. Both retained diagnostic VMs
were then released through their own durable journals; all seven goldens remain.
Bounded private failure diagnostics now include timestamps and at most two
one-second samples of the fixed native probe; no identity/keychain content is read.
Private evidence: `/private/tmp/virfield-sequoia-keychain-retained-8c521fe-20261015`
and `/private/tmp/virfield-sequoia-keychain-retained-r2-8c521fe-20261015`.

Installed `6779b6f` passed exact HTTP/fresh stdio MCP, the full local CI/security
checks and two new complete iPad Monterey native/reboot/workload/RFB/release
cycles. Their post-reboot greetings took about two milliseconds. A live wrong
Tart-container request returned `invalid_request` before reserving a lease.
On HTPC, the first Sequoia passed its complete native/reboot/workload/artifact/RFB
cycle with a 0.11-second post-reboot greeting. The second reached ready in about
eleven minutes, rebooted to a fresh UUID, but verification again ended just before
automatic startup proof: diagnostics at `08:53:22 UTC` contained valid native
ready/result and the new scope/boot startup marker. Both owned leases were
released; Sonoma had not started. This is not a complete provider-matrix PASS.
Source now gives only post-reboot `verify` ten minutes, retaining five-minute
initial `ensure`, one-minute SSH native probing and the original parent deadline.
The real shell loop accepts valid proof beyond five minutes and still bounds
missing proof and time already spent in earlier phases. The same source fixes
the live helper's enclosing SSH wait so it cannot truncate that verification.
This observation-only correction needs a new installed repeat.
Private evidence: `/private/tmp/virfield-installed-htpc-native-6779b6f-20261015`
and `/private/tmp/virfield-installed-ipad-monterey-6779b6f-20261015`.
An additional simultaneous Swift compile attempt was interrupted by that test's
own scheduled Monterey reboot; it is not counted as a compile PASS. Xcode/Swift
versions were reported, and the earlier `8c521fe` compile/run receipt remains
accepted for the unchanged golden.

The physical HTPC lease passed on `c85a899`: acquire/replay, exclusive-host
refusal of a second lease, pinned SSH workload, artifact export and release.
After release its own workspace was absent, the baseline `authorized_keys` hash
was unchanged and the caller key was rejected. The physical node's SSH service
remained available as required; an initial runner assertion that its direct
port must close was corrected through read-only post-release verification.
No physical host reboot was dispatched. Evidence:
`/private/tmp/virfield-installed-htpc-host-c85a899-20261015/journal.json`.

The first factory attempt `image-b3dba0ef5874ab2b2d3e8ee4492e5270` failed before
publication: APFS resize succeeded to 120 GB, then immediate root `df` still
reported 14 GB free. Packer exited and removed its VM; logs and the operation
were preserved before exact-name journaled cleanup. Source now checks writable
Data capacity with bounded observation after a single resize. Delayed capacity,
stale-root, shortage and invalid-statistics fixtures pass; live verification of
the correction subsequently passed on factory attempt 2.

Factory/private GHCR roundtrip passed on installed `2f23ae6`: fresh public-base
factory and two worker clones; a separate portable rebuild with public bootstrap
credentials, sanitization and push; verified private package visibility and digest;
pull, qualification, two further clone workloads and confirmed own cleanup.
All seven baseline image IDs/manifests were preserved. The source private golden
was not the exported disk. The private reference is
`mallexxx/virfield-hardening-acceptance:release-check-0644cb90dac8`, digest
`sha256:a5d42b4e6e48641fba1af7171158de39252e4439d3087c1b019f392223054e73`,
21,460,835,255 descriptor bytes. Evidence:
`/private/tmp/virfield-release-roundtrip-20261014/journal.json` and `evidence/`.
The initial failed build and its cleanup remain in the same durable journal.

A real empty-image Podman probe reproduced another permission defect: conversion
created mode `0644` despite the outer SSH shell's umask. In the traversable golden
directory this exposed a temporary private disk before the final chmod. Explicit
OCI `--umask 0077` produced `0600`. Source now sets both shell/OCI umasks and
applies `0400` before publishing the disk, refuses ambiguous existing staging and
cleans only that image's staging on exact-name deletion. Regression tests observe
permissions before rename, not only the final mode. Existing secured golden
permissions remain unchanged pending the separate approval.
The correction is installed as `2724f94`; its unit/command fixtures and real empty
Podman conversion probe passed. This is not a completed factory build on the node.

The old owning MCP adapter was identified by the sole new API socket from this
session's completed read-only status request and stopped without touching other
adapters. The client did not reconnect automatically and now requires an explicit
reconnect; HTTP/fresh stdio acceptance does not replace this gate.

Live controller state restore passed on installed `ab9a2ad`: backup
`backup-24b8cc75c691697893d48d44c6163bfb`, separate private state and new owner
token, old-token rejection, exact HTTP/fresh stdio MCP, and two native Monterey
lease/reboot/workload/release cycles. Read-only SQLite comparison confirmed that
new leases existed only in the restored copy. The existing launchd service was
switched atomically and returned to byte-exact original config/state, preserving
all seven goldens. One manager/listener ran at a time. Runtime disks were not
included. Evidence: `/private/tmp/virfield-live-restore-drill-20261015/report.json`.

Signed controller-binary rollback/forward passed: `2724f94` → `2f23ae6` →
`2724f94`, through the existing single launchd service and listener. Exact server
versions, fresh PIDs, stable Developer identity, original signed binary hash,
unchanged configuration and all seven image IDs/manifests were verified.
The first probe stopped on transient startup inventory; inspection and a recorded
resume accepted the already-running old controller before forwarding, without
dispatching rollback again. Evidence:
`/private/tmp/virfield-controller-rollback-drill-2724f94-20261015/report.json`.
This covers the controller binary, not a full package/client or VM-disk rollback.

Node native acceptance, current-client reconnection, native browser GUI launch
and existing HTPC golden permission migration remain open.
The following sections retain dated evidence from earlier candidates.

## Earlier candidate hardening (2026-10-14)

Candidate `09ad696` was installed on 2026-10-14 with the same Developer identity and
Team ID E3R68ED529, preserving all five image-ready records. Local `make check`
passed and six successive clones (two per configured Tart profile) passed pinned
SSH, cross-key rejection, distinct host keys, exact OS/build/SIP, desktop, artifact
export and confirmed release. Disk reserve correctly prevents two concurrent
full-size disks on the external SSD; this run used sequential clones.
Supplemental candidate `aee89c1` was installed at that checkpoint. HTTP and fresh stdio MCP passed
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
The supplemental storage-routing, inventory and keychain/UI fixes were later
installed after both unrelated leases were released; see the current checkpoint.
Node clone acceptance and private GHCR roundtrip were outstanding at that earlier
checkpoint; the current results above supersede it.
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
| Interrupted Tart inventory | Regression tests reproduced canceled address/container probes returning partial healthy snapshots; refresh now fails on cancellation and Control preserves the last complete inventory while pausing admission; installed in the current candidate |
| Cold Tart transfer | First live factory attempt failed with Packer EOF during source transfer; journaled recovery deleted only its own failed record after process/inventory inspection. Pinned prefetch/retryable resolve and disabled automatic pruning later passed the full private roundtrip recorded above |
| GUI/SSH keychain | Private Aqua/SSH session preparation, retained/rekeyed login items, native WhenUnlocked add/read/delete and fresh boot evidence; Ventura/macOS 27 live fixtures and real DDG CI Downloads smoke passed |
| UI desktop | Persistent actual 1920×1080 frame in Aqua, explicit pixel units in Tart configuration and no window refit; eight-clone native matrix and real Settings/General UI-test smoke passed |
| macOS 27 ASIF factory | Explicit Recovery keep bypasses default deletion/file lock; full isolated source factory build/qualification passed |
| SSH boot banner | 45-second banner budget, timeout classified as retryable connection failure before commands; real local SSH banner regression test |
| Stale MCP process | A connected process still returned historical Lume instructions after binary replacement; installed smoke now checks exact package version and all four runbooks/resources against candidate source. Owning-client reconnection and acceptance remain pending |
| Acceptance replay journal | Atomic private file replacement and fsync preserve the last complete replay record on interrupted writes; changed replay bodies are rejected before dispatch; regression tests |
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

The current source uses an Aqua login agent, a private scoped supplemental keychain,
content-preserving login-keychain rekey, and an SSH ForceCommand bridge that
unlocks immediately before each command. Native add/read/data comparison/delete
must pass in both contexts; qualification checks a fresh post-reboot proof.
Login remains the persistent default and first search entry, so OS clients can
use its automatic-login unlock before the owned agent starts. Earlier scoped
default receipts below retain their historical scope; the latest Spotlight
regression above supersedes their general desktop-readiness claim. Passwords are
not made blank or public.

Tart recipes/clones use explicit `1920x1080px`, independent of host Retina scale.
Default/point units reproduced a 3840x2160 Monterey display without a 1920x1080
mode; the pixel-unit correction passed the eight-clone matrix above. Cached guest modes
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
These fixes are installed; the current installed native matrix is recorded above.
Native tool exit status alone is never the readiness gate.

## Automated candidate results

The complete `make check` passed on code baseline `8c521fe`: documentation links, twelve Python tests,
formatting, `go vet` and the full Go race suite. Three Go binaries and the native
Apple browser built; the browser signature verified. The existing installed
configuration passes the candidate CLI `config-check`. Pinned Staticcheck, module
integrity and current-tree Gitleaks pass; govulncheck results are recorded above.
Live mutation tests remain opt-in and were run separately as recorded above.
The RFB reader correction adds three TCP regression tests (fifteen Python tests
in total); full local CI also passed for that subsequent acceptance-tool change.

Clean local package provenance:

| Source revision | Archive SHA-256 | Deployment |
|---|---|---|
| `558a6cb3d33f0be28e460da15af7696cfaf92ad3` | `cda25f64735340ed8c0de815346c58e1ff4240cd26fbb36ebc32f79e7cddd568` | Installed after idle check; exact HTTP/fresh stdio passed on repeat; intermittent inventory staleness blocked VM dispatch |
| `a6cd900bc9e6dfae071fca65a96920622fe4ce41` | `b979a7aa7ced7a0ef58c5b97cdbf5aab738ed7fcfe76d3422c7a3e60f733d0ed` | Prepared earlier, not installed separately; fresh archive stdio passed; superseded by the runbook checkpoint above |
| `6779b6faa1983a20fbc831fa1b415c110c087c67` | `3ee01fe145444c7da23bef19aa349c8ce007b772c85b3149ffcba73fd2af569d` | Installed; exact MCP, two Monterey cycles and one full Sequoia cycle passed; second Sequoia exposed the five-minute post-reboot verification bound |
| `8c521fe1a6c25656368810c0c8fb820b6927e5a0` | `282064aa6829d4b6c33600bc7e541d2ddc7956a89799ecc2a2a3615d74cd44db` | Installed; exact MCP and two fresh iPad Monterey native/reboot/workload cycles plus Xcode/Swift passed |
| `c85a89974a4d2e707e732cfd3c15eaf92a5b4e7e` | `75dea2278095f682706acb5eb67aa9d933a53b2a79e00e6c9d119786112d373a` | Installed earlier; SSH bounds and physical HTPC lease passed; two staggered native Sonoma cycles passed after a failed simultaneous boot |
| `083801895ec9d78b80328645436801b4215dbfdb` | `a74d66420b935ab5a3e076e24a298458f93c76658de683957dcadc91f775f0e6` | Installed; exact MCP passed; outer SSH command budget now follows the existing job deadline |
| `7e9c5ad6e2f1f331b102dc10819c5ca12d0bc37f` | `b848c75a6f97816f979c2107b8873edf623ff7430424e16f6e963082db9512b1` | Installed; exact MCP and two native Sonoma reboot cycles passed; overall runner stopped on desktop HTTP503 |
| `edcb7563f8aa02a019ff4cb8e575d8daabc2bd2d` | `82b7f7199948ba117a58a4e5067f4b12c56ab7811919038f2eadda7f8a0f80c1` | Installed; exact MCP passed; both node clones initially ready, post-reboot acceptance still failed |
| `2724f94ed52696e5482ad8431a19d1a62a427e0b` | `7cff304d53dd3a59a1aed59fe25907d88495043a66bbb7c3f64ad07813bbe6d4` | Installed; MCP and controller rollback/forward passed; node repeat exposed short GUI observation deadlines |
| `2f23ae609a4f650130f5c84835f89931d6e70592` | `9defb127d54a06c722d403e02ce90b2b427b2597613d51145d6eba2b81de60a1` | Installed earlier; complete factory/private GHCR roundtrip passed |
| `4a53b67ce04fd1adc164750b47e780c4bdde9ea6` | `a71ceaf53dae64c2c91545cdfcff5749c656ba69677411fb154c3a3e6e301f19` | Installed; eight-clone Tart native acceptance passed |
| `8e07015cde200a41fee6b490da97a44437803d7f` | `63c697126d63d3be3160ce57848d0338d2dfd1fe04a2ebbd267e105103b510e6` | Installed; split-state browser trust and exact-version HTTP/fresh stdio MCP passed |
| `aee89c1ffafb01faccc750b60d0e3470bf14fc97` | `2313602b2c380e5e74640c47ee6ad50d9c10d750f856a0949833a4489803cf35` | Installed; signed binary hashes retained in the private upgrade record |
| `d2daf4ee613739686e2227a29e513d3e1a592c9b` | `6d4a01acb5baddf3d441f5c6f5e557125046ce45f74d4ccf03b37c1c46039c2b` | Historical package, superseded by later installations |
| `624bdfa7a88738809729e6889c679eaf0241eb78` | `be29654ef02f4ab6cd747b1815c2a72532717aaf2d423a2a4d8a93e4013634bb` | Historical keychain/UI package, superseded by later installations |

These rows are dated package snapshots. The keychain package includes the
interrupted-inventory fix; subsequent source changes must be packaged again.

Offline restoration of `backup-5cdef526c555f6a53ed5f565c6e2edaf` passed into a
separate private directory: 18 copied files, SQLite integrity, all five active
image identities and their credentials/verification, unchanged node management
key, rotated owner token and configuration validation. This older offline case
did not start a daemon; the newer live controller-state restore is recorded above.
Full VM-disk recovery and package/client upgrade/rollback remain separate gates.

[Live acceptance runner](../tools/live-acceptance.py) persists request keys before
dispatch, retains a private journal, and deletes only its own accepted disposable
leases after explicit cleanup approval. Its no-approval and lost-reply behavior
are tested, along with interrupted journal replacement and changed-replay refusal.
The Go `TestLiveLeaseSSH` also uses the actual guest user/port, accepts
Tart APFS encryption at rest with FileVault off/unlocked, and checks desktop
revocation alongside SSH, cross-key/image-key rejection, workload and artifacts.

## Remaining release acceptance

- Repeat exact-source local CI and clean packaging for subsequent code changes;
  remote CI is deferred by the operator. Existing package provenance is recorded above.
- Node SSH cancellation bounds and inherited-desktop preflight are installed;
  complete HTPC native/resource/reboot/UI acceptance with disposable clones.
  Fresh native Monterey acceptance passed on the corrected guest code.
  Node conversion privacy and overall preparation corrections are installed;
  factory roundtrip and controller-binary rollback/forward passed.
- Install the clean post-reboot verification-budget candidate after unrelated
  leases/jobs are actually clear, then repeat the complete HTPC matrix and exact
  installed MCP acceptance. The prepared archive smoke does not close this gate.
- Reconnect the actual owning MCP clients after installation and verify their
  version, runbooks and workflow; a fresh adapter alone cannot attest old sessions.
- Accept the native Apple browser GUI/URL-scheme flow after the Mac is unlocked;
  compiled trust checks and registration do not replace GUI acceptance.
- Complete permission migration for existing HTPC secured goldens after approval;
  separate authorization is still pending, and read-only inspection confirmed mode 0444.
- Preserve the accepted private GHCR roundtrip receipt above; repeat this gate
  only when relevant factory/export/import behavior changes.
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

- Tart VS Code debug, restart during build and a full
  lifecycle crash matrix; prolonged soak and VM-disk disaster recovery.
- Orchard lifecycle, guest credential isolation, forwarding, worker restart/drain.
- Current candidate workflows in both Codex and Claude Code; full apple-browsers
  UI-test workflow through Broker. The accepted Broker smoke is narrower.
- Remote/container TLS and routing through the intended production entrypoint.
- Native URL-scheme GUI launch, complete package/client upgrade/rollback and
  VM-disk disaster recovery. Controller-state restore and controller-binary
  rollback/forward passed as recorded above.

## Release procedure

A new release requires a clean committed source revision; all CI/release checks
must pass for that exact revision. Build with `tools/release.py NEW_VERSION
--output NEW_DIRECTORY`; it packages committed source, fresh macOS/arm64 binaries,
the native browser, dependency notices, hashes and a revision manifest. Existing
outputs/tags must not be overwritten. Validate extraction, signatures, URL scheme,
installation, live workflows and cleanup. Publishing/VM deletion require their
own authorization. The only published archive is historical v2.0.0 (2026-09-30).
