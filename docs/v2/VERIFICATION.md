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

## Not yet accepted / production release gates

- Browser interaction/visual QA and remote GitHub Actions execution.
- Managed tunnels and Broker/container routing. Per-lease SSH isolation and
  caller-owned key delivery are accepted, but are not the full Broker integration.
- Image registry pull, full legacy tool/Xcode/provider provisioning (including
  Gatekeeper/AMFI/TCC recipe migration), CPU/RAM quotas beyond the
  two-VM cap, and support for macOS builds other than the pinned `26A428` recipe.
- Actual daemon crashes during clone/install/delete, disk-full recovery,
  backup/restore rehearsal, log/artifact retention and prolonged soak testing.
- Production cutover from v1, launchd installation, network/TLS policy and
  per-principal HTTP MCP access. Existing v1 services/configuration were not replaced.

The three unrelated VMs (`macos-15-golden`, `pdf-hud-macos27`,
`uitest-26.4.1-golden`) were left stopped and untouched. Host SIP was not changed.
