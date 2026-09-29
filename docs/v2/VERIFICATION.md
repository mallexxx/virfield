# Verification record — 2026-09-29

This record distinguishes automated checks, actual VM acceptance and remaining
production gates. It does not certify the entire planned v2/Broker/Runner stack.
All timestamps below are UTC. Host: Apple Silicon macOS; Lume 0.5.3; Go 1.26.8.

## Automated checks

- `make check`: Python Assistant/Recovery regression tests, gofmt, `go vet`, full
  `go test -race ./...`. Live destructive tests skip unless explicitly enabled.
- `make build`: `virfieldd`, `virfield`, `virfield-mcp`, darwin/arm64.
- Staticcheck v0.7.0: passed.
- `govulncheck` v1.6.0: zero reachable vulnerabilities and zero vulnerabilities
  in imported packages. One advisory exists in a required module's unimported
  OpenPGP package; this is not a zero-advisory module inventory.
- JS syntax and launchd plist syntax: passed. This is not browser/launchd acceptance.

Go 1.26.8 is selected by the project without modifying the Homebrew installation.
The SSH implementation uses `golang.org/x/crypto v0.56.0`; `x/sys` is v0.47.0.
Local HTTP/SSH fixture tests require permission to bind loopback sockets. Sandbox
`bind: operation not permitted` was resolved by running checks with that permission.

Tests cover admission races, external/unknown capacity, idempotency, SQLite
rollback/reopen/migration, ambiguous clone quarantine, delayed starts, expiry,
cleanup failures, strict HTTP/auth/origin contracts, official MCP SDK calls,
redirect credential protection, image checkpoints, immutable build profiles,
range download integrity, pinned SSH host keys, physical-host rejection, bounded
SSH/process output, cancellation of owned subprocess groups, and real observed
Assistant/Recovery text regressions.

## Live lifecycle: passed

`TestLiveLifecycle` passed in **49.943 seconds** at approximately 13:24:

1. Two disposable clones of the then-existing `macos-27-golden` reached
   running + Lume-reported SSH readiness.
2. A third request returned `capacity_exhausted`.
3. Reopening SQLite preserved the leases and idempotency responses.
4. Both test VMs were stopped/deleted and absence was observed.

Journal: `state/v2-live-20260929-b/state.db` (private, ignored by Git).
An earlier attempt in `v2-live-20260929-a` paused when the independently running
Lume service disappeared. The cause was not established. The manager retained
reservations; after restoring Lume separately, exact-VM operator recovery cleaned
up those two test clones. No unrelated VM was removed.

## Original golden deletion and fresh Apple download: passed

The user explicitly authorized deleting `macos-27-golden` through v2 and testing
image preparation. The original image was deleted by v2 job
`job-756d3304d77d05f7164bc206abbcf8cb`; absence was confirmed at 13:38:12.

Build `job-08b447cc5082359bbda6c6b6a16afac6` downloaded **26,626,436,228 bytes**
from the pinned Apple HTTPS URL. It did not copy the host's existing IPSW.
Two controlled daemon restarts during download exercised HTTP Range resume.
Full SHA-256 verification passed:

```text
2a5d3c695d501022b7fad9adaffcf2627bcb867d993fb5662dcd41bac99a2836
```

Lume installed macOS 27 build `26A428` and completed native offline setup. Live
inspection exposed and fixed these recipe assumptions:

- Setup Assistant age-range OCR can omit the title; Liquid Glass needs the
  bottom Continue button; final Get Started needs its own OCR region.
- Apple VNC can withhold unchanged framebuffers; reads reconnect for a full frame.
- Recovery starts with a temporary 1280×720 framebuffer before 1920×1080.
- Native Lume SIP navigation opened Time Machine while the language selector
  was still active. The replacement driver explicitly selects English and
  verifies Utilities → Terminal before issuing any shell command.
- Sparse OCR misses the picker Continue button; its label is checked separately.
- macOS 27 reports `System Integrity Protection is off` in Recovery, while
  normal boot reports the canonical `System Integrity Protection status: disabled.`
- Password changes use macOS `sysadminctl` with the old credential; automatic
  login is reconfigured with explicit administrator authentication.

The first build needed inspected stage retries during development. It finally
passed normal-boot build/SIP checks, credential rotation, effective password-free
SSH configuration, key-only login and Finder after a further reboot. Verification
was recorded at **14:54:50**, then the image was stopped and promoted. This is
stage-level live evidence, not an uninterrupted build acceptance claim.

## Clean API image rebuild acceptance

The follow-up `TestLiveImageRebuild` uses only the deployed v2 HTTP API. It deletes
the exactly named golden when present, re-hashes the verified cached IPSW, builds
a fresh VM without stage recovery, then verifies two-clone admission and cleanup.
A cache hit in this repeat is distinct from the actual network download above.

The first clean attempt (`job-66ae4063f6e2feea9973eec8450349c7`) exposed an
additional process-lifecycle bug: after successfully clicking Get Started,
Twisted worker threads kept the Python process alive. Both VNC entrypoints now
call `api.shutdown()` in `finally`, including successful completion. A regression
test covers success and failure cleanup. That interrupted attempt is not counted
as passed; its exact test VM was cleaned up through v2 operator recovery.

**PASS: 638.48 seconds (10 minutes 38 seconds), without any stage retry,
manual guest input or daemon restart.** Final clean run:

- Build job: `job-0150643ce4403ae072c6ce4a9ed474f9`.
- Image record: `image-1488b3ebbe8da8e1956236c90dde9dce`.
- Accepted at 15:07:39; image verified at 15:17:23 and promoted at 15:17:31.
- Maximum observed v2 API read latency across this test: **3.937125 ms**.
  This is one local run's measurement, not a load/soak performance guarantee.
- Two disposable clones reached readiness; request three returned
  `capacity_exhausted` with `Cannot run more than 2 macOS VMs: 2/2 slots occupied
  or reserved`. Idempotent acquire returned the first existing lease.

Both clones were stopped/deleted and their absence checked. Final inventory has
four stopped golden/existing VMs, zero active jobs, one `image_ready` record and
**0/2 occupied slots**. The rebuilt `macos-27-golden` is retained as a stopped,
verified base image. Its private `verification.json` records exact build, media
hash, canonical disabled SIP, Finder and image-specific SSH success after reboot.

The foreground v2 test daemon is stopped after acceptance; this is not a launchd
production deployment. Its private journal/configuration are retained under
`state/v2-live-20260929-a`. Lume remains an independently managed process.

## Scoped lease SSH acceptance

**2026-09-29, PASS:** `TestLiveLeaseSSH` against the retained verified golden.
The first run completed in **72.63 seconds**. The expanded run completed in
**99.69 seconds**, including a real SIGKILL/restart of the foreground v2 daemon
while both disposable clones were ready. Expanded run leases:

- `lease-46da81dcb9c66d8d31a8a16fd6b8e604`.
- `lease-5327d42fe8275dff74bf76a4bfb40ccc`.

The expanded test proved:

- Distinct per-lease Ed25519 host keys, successful own-key authentication,
  rejected cross-lease keys, and rejected inherited image keys. Negative probes
  use the correct NEW host pin and require an authentication refusal; a network
  failure or changed host pin cannot count as a successful denial.
- Administrator password and management key differ from the image credentials;
  status/events/lease API responses do not contain the inspected private secrets.
- A hard daemon restart preserves both ready records and exact SSH metadata.
  Own-key authentication and the generated strict OpenSSH configurations work
  after restart. Finder, disabled SIP, virtual hardware and workspace probes pass.
- Third admission returns `capacity_exhausted`; acquire retry returns the original
  lease; API release stops/deletes both test-owned VMs and restores 0/2 slots.

The SSH implementation also checks exact authorized keys and effective sshd
policy before publishing readiness. Automated controller tests cover a blocked
SSH worker, concurrent renewal, expiry, failure and interrupted-stage recovery
without replaying credential mutation. Schema 3 rejects older readers. Race tests,
vet and Staticcheck passed. Govulncheck found zero reachable/imported-package
vulnerabilities; one advisory remains in an unused part of a required module.

The hard crash above happened after readiness. It does **not** establish crash
safety during clone, install, SSH mutation or delete; those live fault-injection
gates remain. Private daemon diagnostic/credential retention still needs a policy.

```sh
VIRFIELD_LIVE_LEASE_SSH=I_APPROVE_TEMPORARY_VM_DELETION \
VIRFIELD_LIVE_TEMPLATE_ID=live-test \
VIRFIELD_LIVE_TOKEN_FILE="$PWD/state/v2-live-20260929-a/token" \
VIRFIELD_LIVE_STATE_DIR="$PWD/state/v2-live-20260929-a" \
  go test -v ./internal/client -run '^TestLiveLeaseSSH$' -count=1 -timeout=17m
```

The test requires an idle already-running v2 daemon and leaves the golden intact.
For the restart variant, set `VIRFIELD_LIVE_RESTART_MARKER` to a NEW local path.
When the test creates that file, restart only its test daemon, wait for successful
reconciliation, then create `<marker>.resume` within two minutes. The harness
never kills an external process itself.

## Installed-service hardening — 2026-09-29 17:00 UTC checkpoint

The replacement is staged at `/Users/admin/.virfield-v2`, independently of the
checkout: four Go binaries, schema 4 database, private token/image identities,
APFS-cloned IPSW cache, pinned isolated Python VNC dependencies and migration
backups. Host quotas are 12 CPUs, 48 GiB RAM and 20 GiB disk reserve. The base
image profile remains unchanged; full UI-test provisioning was **not applied**.

Accepted on the installed daemon:

- HTTP and stdio MCP discover ten tools and return status; anonymous HTTP MCP
  returns 401. Latest `TestLiveInstalledMCP` completed in 0.48 seconds.
- Headless Chromium desktop 1280×1000 and mobile 390×844 login/render checks:
  token field clears, 0/2 pool renders, no horizontal overflow or console errors.
  Private screenshots: `state/v2-live-20260929-a/ui-{desktop,mobile}.png`.
- API backup `backup-b6ee05eada6d4d9d1b54e928dc1febac` restored into an isolated
  temporary directory. SQLite integrity, retained image identities/verification,
  token equality and 0600 database permissions passed. This is a control-state
  restore rehearsal, **not** a VM disk disaster-recovery test.
- Automated CPU/RAM/disk fail-closed admission, lease tunnel revocation,
  incomplete-backup cleanup, five-snapshot retention, SQLite real
  `max_page_count` disk-full rollback, and bounded log rotation/recovery tests.
  Daemon and Lume normal output rotate at 10 MiB with three archives; released
  lease private credentials are deleted only after confirmed VM absence.
- `make check`, four-binary build, Staticcheck, Linux cross-build and govulncheck
  passed. Govulncheck still reports zero reachable/imported-package
  vulnerabilities and one advisory in an unused required-module package.

**LaunchAgent SSH acceptance failed twice and is not counted as passed.** Lume
reported SSH available, but the installed agent received `no route to host` for
both guest IPs; direct terminal TCP probes immediately reached OpenSSH. Both
failed runs cleaned up their own disposable VMs. The diagnosed deployment issue
is macOS Local Network Privacy: Apple's TN3179 distinguishes GUI agents from
system daemons. A system LaunchDaemon running as `admin` is prepared, with a
separate Lume service. The macOS administrator dialog is still pending at this
checkpoint. No host TCC/SIP database or global network policy was modified.

The fixed `uitest-27-v1` guest profile adds Xcode transfer/first launch, tools,
Gatekeeper/AMFI/TCC, sudo policy and post-reboot Swift/Peekaboo permission probes.
Automatic approval review rejected its live application without explicit consent
to these exact guest security changes. The request for that consent is pending;
its implementation must not be described as accepted or silently enabled.

## System LaunchDaemon and v1 cutover — accepted

After the user's macOS authorization, both `ai.virfield.virfieldd` and
`ai.virfield.lume` were registered under `/Library/LaunchDaemons`. Both run as
`admin:staff`, not root. The old GUI LaunchAgent plists were moved to the private
migration backup. The system daemon successfully prepares and authenticates
against guest SSH; the Local Network Privacy deployment failure is resolved.

**PASS: `TestLiveLeaseSSH`, 75.80 seconds**, through the installed system service:

- Leases `lease-58126c922d9f31541f961d9cc316b68c` and
  `lease-1c03d438fceec87f53839ad4a0bf3fd2` both reached authenticated readiness.
- Request three returned the explicit `Cannot run more than 2 macOS VMs` error;
  acquire replay retained the original lease.
- A real SIGKILL of the exact system daemon was recovered by launchd. After
  fresh post-restart Lume inventory, both ready leases and pinned identities
  were unchanged. Own keys and SSH tunnels authenticated; cross-lease and image
  keys were rejected using the correct new host pins.
- Finder and SIP probes passed. Both owned clones were deleted, their private
  credential directories removed, and tunnel listeners closed. Pool returned 0/2.

An earlier 122.39-second run resumed immediately when the daemon's HTTP listener
returned, before its first inventory observation. Tunnel creation correctly
returned `backend_unavailable`. Its clones were cleaned up. The passing run's
restart coordinator waited for a successful observation newer than SIGKILL,
with both running VMs, before releasing the test checkpoint. This was a
coordinator ordering correction, not relaxed daemon admission.

Only after that pass, the Eagle `/api/services/virfield` entry was deleted through
its supervisor API, removing its old backend and autostart. Port 3000 is closed;
Lume 7777 and v2 7780 remain open on loopback. All observed old TypeScript MCP
processes were stopped by exact command/PID match; a subsequent scan found none.
Eagle itself and unrelated services were not stopped.

Codex `~/.codex/config.toml` and Claude `~/.claude.json`, `~/.claude/mcp.json`
now point to `/Users/admin/.virfield-v2/bin/virfield-mcp` with the private token
file argument. Existing application sessions may need restart to rediscover
server tools. No token was placed in a command argument or configuration text.

After cutover, `TestLiveInstalledMCP` passed in 0.54 seconds, desktop/mobile UI
checks passed again, and backup `backup-edbc0c07da76c673c58e706420ae6b9d` completed.
All current local implementation code was committed as `b8df85d`; publication
was rejected by automatic approval review pending explicit remote-export consent.

## Interrupted SSH preparation — accepted

**PASS: 34.02 seconds**, lease `lease-25e3cceeb4503ac0634df6300a7cde82`.
The exact v2 system daemon was SIGKILLed while its durable job was
`ssh_dispatched`, after its private lease identity had been recorded. Launchd
restarted it; the lease became `needs_attention` with `ssh_outcome_unknown`, no
published SSH connection and its slot still reserved. The stage was not replayed.
The ordinary API release then stopped/deleted only that owned clone, removed its
private identity directory and restored 0/2 with no active jobs. This validates
interruption of the SSH preparation stage, not arbitrary points in every guest
shell operation. Private test harness is retained at
`state/v2-live-20260929-a/ssh-stage-fault.py`.

## Full UI-test golden profile — accepted after explicit authorization

The user explicitly authorized guest-only Gatekeeper/AMFI disable, TCC grants and
passwordless sudo, and required these to run automatically during golden setup.
`macos27` now selects `uitest-27-v1`; the host's SIP/TCC/Gatekeeper and selected
Xcode are unchanged. Host Xcode 27.2 beta (27B5019j) is copied over pinned SSH
into the guest. Source paths, commands and guest policy remain operator-owned.

Job `job-1f9ee4fe1208db779fd40beffda19f6e` provisioned the retained image
`image-1488b3ebbe8da8e1956236c90dde9dce`. Final post-reboot verification passed at
**2026-09-29T17:58:46.730609Z**, followed by stopped-image promotion. This run used
inspected recovery while developing the fixes below; it is not an uninterrupted
fresh-build claim.

Live failures exposed and corrected:

- Xcode 26.6 is incompatible with the macOS 27 Homebrew tool profile. The source
  is now checked before media download; Xcode 27+ is required, and guest selection
  is restored after Homebrew installs CLT. Existing Xcode can be reused only after
  exact version comparison and deep strict code-signature verification.
- The old `EnableAssessment=false` preference did not disable Gatekeeper.
  Apple's `enabled` CFString set to `no`, readable preference permissions and
  post-reboot `spctl --status` verification now establish the effective policy.
- macOS 27's user TCC database is in a protected container, not the assumed HOME
  path. The recipe resolves the database opened by the registered GUI-user tccd,
  validates UID/path/schema, waits for read-only database discovery and fails
  provisioning on any failed grant. It never publishes a partially granted image.
- Lume's readiness can precede an actual reachable SSH endpoint. Only TCP connect
  failures get a bounded 90-second readiness wait; host-key, authentication and
  command failures never trigger replay or weaker authentication.

Verification checks Xcode, executed Swift, required tools, passwordless sudo,
Gatekeeper, AMFI boot arguments, Terminal TCC grants, System Events and Peekaboo's
actual required local permissions. The enclosing image checks SIP, build, Finder,
key-only SSH and reboot. An explicit `reprovision` recovery action can repair a
failed verification; ordinary verification retry does not replay provisioning.
Controller tests prove a newly built golden executes the full stage order and
cannot be published or leased if provisioning or verification fails.

**PASS: `TestLiveLeaseSSH`, 89.98 seconds**, through the installed system daemon:

- `lease-d793cbc4aafaade28ce5fc0d9f699f58` and
  `lease-376f5685ff6185f97cdc457bc76d1443` inherited the complete profile.
- Both executed Swift, verified Gatekeeper/AMFI and required Peekaboo permissions,
  controlled System Events, and created nonempty screenshots in guest workspace.
- Own-key SSH and tunnels passed; cross-lease and image keys were rejected.
  The third lease was refused at 2/2; idempotent replay retained the first lease.
- Both disposable VMs, daemon-held lease credentials and tunnel listeners were
  removed. The verified golden remains stopped and available.

Private verification and stage logs are in the installed state's image directory.
Homebrew packages resolve from their taps at build time: the recipe is versioned,
but package resolution is not a complete reproducible dependency lock.

## Not yet accepted / production release gates

- Remote GitHub Actions execution, remaining VM-mutation crash scenarios,
  prolonged soak and VM-disk disaster recovery. Unit/mock coverage is separate.
- Registry pull, other macOS builds, remote TLS/container routing and
  per-principal credentials. Broker/Runner/Balda integration is a later scope.

The three unrelated VMs (`macos-15-golden`, `pdf-hud-macos27`,
`uitest-26.4.1-golden`) were left stopped and untouched. Host SIP was not changed.
