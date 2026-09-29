# Virfield v2 — control-plane implementation

**Branch:** `codex/virfield-v2`. **Status:** lifecycle core and pinned base-image pipeline implemented; production release gates remain.

This implements the first vertical slice of §13/§16 stage 2 in the
2026-09-29 Balda/Callee/Prism plan. Broker, Runner and Balda integration remain
separate applications. The TypeScript application and existing VM data are
preserved as migration references. No v1 service or MCP configuration is replaced
by building v2.

## Run locally

Requires Go 1.26.8 or newer (automatically selected from `go.mod`) and the independently running Lume 0.5.3 service.
Use a **new** state directory; do not point v2 at `~/.virfield/state.db`.

```sh
make check
make build
# DIRECTORY must not already exist. Existing files/tokens are never overwritten.
./bin/virfield init "$PWD/.v2-state" macos27 macos-27-golden home
./bin/virfieldd -config "$PWD/.v2-state/config.json"
```

In another terminal:

```sh
./bin/virfield -token-file "$PWD/.v2-state/token" status
```

Open `http://127.0.0.1:7780` and enter the token from `.v2-state/token`.
The page keeps it in memory only. `status` and the console read SQLite plus the
last reconciler observation; polling clients do not repeatedly call Lume.

### Acquire, inspect and renew

```sh
./bin/virfield -token-file "$PWD/.v2-state/token" -key story-42-attempt-1 acquire macos27 3600
./bin/virfield -token-file "$PWD/.v2-state/token" lease LEASE_ID
./bin/virfield -token-file "$PWD/.v2-state/token" job JOB_ID
./bin/virfield -token-file "$PWD/.v2-state/token" events 0
./bin/virfield -token-file "$PWD/.v2-state/token" renew LEASE_ID 2026-09-29T18:00:00Z
```

The expiry example must be replaced with a future RFC3339 timestamp within
24 hours. Reusing an older deadline never shortens an active lease. Reusing an
acquire key returns the same lease, even if it has subsequently been released.
Reusing it with a different request returns `idempotency_conflict`.

**Release and expiry permanently delete the disposable VM.** Export artifacts
first. The UI asks for confirmation; CLI/API clients must obtain authorization
before release. Acquiring a lease authorizes its expiry cleanup as part of that
lease contract.

```sh
./bin/virfield -token-file "$PWD/.v2-state/token" -key story-42-release-1 release LEASE_ID
```

Only generated VM names recorded in this v2 database can be cleaned up. Golden
images and arbitrary external VMs are never adopted by name.

## Modules

| Module | Responsibility |
|---|---|
| `cmd/virfieldd` | Configuration, host singleton lock, HTTP server, shutdown |
| `internal/control` | Admission, leases, durable job phases, reconciliation, cleanup |
| `internal/images` | Pinned IPSW downloads, versioned guest provisioning, image verification |
| `internal/store` | SQLite schema, atomic state + event transactions, idempotency |
| `internal/lume` | Sole Lume HTTP/CLI adapter, fixed image commands, no blind mutation retries |
| `internal/httpapi`, `internal/web` | Versioned authenticated API, embedded console |
| `internal/client`, `cmd/virfield` | API-only CLI |
| `internal/mcpadapter`, `cmd/virfield-mcp` | Official Go SDK stdio MCP adapter |
| `internal/config`, `internal/hostlock` | Validated config/token, single host owner |

There is no personal SSH key discovery, host shell endpoint, PAT storage,
process-name scanning or automatic Lume restart in v2. Fixed image jobs use
allowlisted Lume CLI operations and a versioned guest VNC script. The initial
Lume password is confined to fresh-image bootstrap and is rotated before image
verification. API clients have no direct store or Lume dependency.

See [Image Manager](IMAGE-PIPELINE.md) for profiles, build, recovery and deletion.

## Capacity and recovery

1. Acceptance reserves a slot under the controller mutex and commits it before
   returning HTTP 202. The cap is `min(config.max_vms, Lume.max_vms, 2)`.
2. Capacity is the union of active observed VMs and unreleased leases, plus any
   additional occupied slots reported by Lume. Stopped managed VMs still reserve
   their slot until cleanup completes. External VMs count against capacity.
3. Unknown or stale inventory blocks acquisition. Unknown VM states consume
   capacity. Backend failures never become an empty list or permission to start.
4. Each external effect has a durable dispatch phase. A lost response never
   causes an automatic clone/start/stop/delete retry. Start is asynchronous:
   HTTP 202 is not readiness. An early `stopped` observation does not permit
   deleting a disk while a start might still complete.
5. Expiry queues cleanup in SQLite. Preparation cancellation, cleanup acceptance
   and its event are atomic. Capacity is freed only after a fresh observation
   confirms absence, with no unconfirmed start or clone outstanding.

A kernel file lock prevents two v2 daemons for the same OS user, even with
different ports/databases. External Lume callers remain outside that lock:
production operation requires that v1 and other writers do not manage these VMs.
Lume/Virtualization.framework remains the final hardware admission authority.

`ready` means running + valid guest IP + **Lume-reported** SSH availability.
It does not prove that Broker's future scoped SSH credential authenticates.

### When a job needs attention

Read its error and inspect the named VM in Lume. An interrupted clone is
quarantined because an existing directory can be an incomplete disk copy.
Do not edit SQLite or delete the state directory to free capacity.

After confirming that Lume has **no operation in flight** for the exact VM,
the operator can authorize cleanup:

```sh
./bin/virfield -token-file "$PWD/.v2-state/token" -key inspected-lease-resolution-1 \
  resolve LEASE_ID EXACT_VM_NAME CONFIRM-NO-OPERATION-IN-FLIGHT
```

This operation is absent from MCP. It requires a fresh observation after the
last mutation. It never retries preparation. Unowned name collisions and
protected golden images cannot be deleted through this path. If Lume remains
unavailable, leave the slot reserved and restore the service separately.

## MCP configuration

```json
{
  "mcpServers": {
    "virfield-v2": {
      "command": "/absolute/path/to/virfield/bin/virfield-mcp",
      "args": ["-token-file", "/absolute/path/to/virfield-v2/token"]
    }
  }
}
```

Tools: `virfield_status`, `vm_acquire`, `vm_lease`, `vm_release`, `vm_renew`,
`virfield_job`, `virfield_events`, `image_build`. A full pool returns `capacity_exhausted` with
an actionable message. **Queueing belongs to Broker**, not this host daemon.

The daemon currently binds only to loopback. Container/Broker access needs an
explicit TLS reverse proxy and network policy. The client rejects remote
cleartext HTTP and never follows redirects with its bearer token. HTTP MCP
hosting and per-principal credentials are not implemented yet.

## launchd

`deploy/ai.virfield.virfieldd.plist.example` is a reviewed deployment template,
not automatically installed. Replace all absolute-path placeholders, validate
with `plutil -lint`, and use a dedicated private state directory. Lume is managed
as its own service; Virfield never restarts or kills it. Arrange log rotation
before production deployment. The token must be owner-only (0600) and state
directory private (0700).

## Verification

```sh
make check                       # formatting, vet, race tests; no live VM mutations
make build                       # three Go binaries
VIRFIELD_LIVE_LUME_URL=http://127.0.0.1:7777 go test ./internal/lume -run TestLiveReadOnly -v
```

The test suite covers concurrent admission, external/unknown slots, idempotency,
SQLite rollback/reopen, clone quarantine, restart recovery, delayed starts,
expiry, cleanup failure, drift, token/origin checks, strict JSON, SDK tool calls,
redirect credential protection and Lume HTTP request contracts. The live
read-only check validates only host status and inventory.

**Live lifecycle accepted:** two real clones reached Lume SSH readiness, a third
request was refused, SQLite reopen preserved leases, and both clones were deleted.
**Base-image acceptance passed:** a real Apple download with resume and SHA-256,
followed by a clean cached-media rebuild through Assistant, Recovery/SIP, SSH
rotation and reboot verification; two clones and cleanup passed in 10m38s.
See [the evidence and limitations](VERIFICATION.md).
**Not yet accepted:** daemon crash injection during actual VM mutations; disk-full recovery;
long-duration stability. Automated mock tests do not establish these claims.

## Remaining planned modules and release gates

| Planned scope | Current state / release gate |
|---|---|
| Core lifecycle, API, CLI, stdio MCP, minimal UI | Implemented; two-VM live lifecycle passed |
| Image Manager: download/pull/build/promote | Pinned download/build/verify/promotion implemented; registry pull pending |
| Versioned provisioning jobs | Native Lume setup plus verified macOS 27 Assistant/Recovery drivers; full tool/Xcode provisioning pending |
| Scoped SSH credentials, verified guest login, tunnels | Image-specific credentials implemented; per-lease rotation/delivery and tunnels pending |
| Resource quotas beyond two VM slots | CPU/RAM/disk admission still required |
| HTTP MCP, Broker-facing deployment | HTTP API exists; TLS/container routing and scoped principals pending |
| Production operations | Backup/restore rehearsal, retention/log rotation, soak/fault injection pending |
| Broker/Runner/Balda/Prism | Separate later stages; not part of this implementation |

### Why the old scripts are not automatically wired in

The scripts currently call Lume themselves, write JSON state, use `lume/lume`
credentials and select host directories. Running them unchanged as a generic
"job" would recreate the exact multiple-owner and credential problems being
removed. The image manager uses a versioned, allowlisted manifest, fixed executable/arguments,
bounded logs, image-specific credentials and a single lifecycle owner. Preserve their provisioning knowledge, not their
orchestration authority. Do not add a public "run script" or "SSH exec" escape hatch.

## Sources and implementation rules

- [Effective Go](https://go.dev/doc/effective_go) and [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).
- [Lume CLI reference, documented for 0.5.3](https://cua.ai/docs/reference/lume/cli-reference).
- [Serve the Lume API](https://cua.ai/docs/how-to-guides/lume/serve-api).
- `lume-0.5.3-api.json`: output of the installed `lume dump-docs --type api --pretty`.

The installed server also exposes `/lume/host/status`, which is **not in that
API dump**. Its required capacity fields were checked live on 2026-09-29. The
reported `version: 1.0.0` is not the CLI version; do not compare it to 0.5.3.
Malformed/missing fields fail closed. Revalidate this contract on Lume upgrades.

Use contexts and deadlines for all I/O; wrap errors at boundaries; expose safe
machine error codes, never raw upstream response bodies; keep dependency
interfaces at the consumer; format with gofmt; run vet and race tests. Persist
intent before effects, never fake exactly-once external execution, and never
hold the admission mutex across network I/O. Add tests for failure behavior,
not tests that only mirror implementation details.

The minimum Go patch is pinned after `govulncheck` found reachable standard-library
vulnerabilities in the installed Go 1.26.2. Go toolchain selection downloads a
project toolchain; it does not replace the Homebrew installation.

## Live mutation acceptance (requires operator authorization)

This test is prepared but **not automatically run**. It clones the selected
stopped golden image twice, starts both, waits for Lume-reported SSH readiness,
verifies refusal of a third lease, reopens the database, then stops/deletes only
its own two disposable VMs. Expect **5–15 minutes**, with an 18-minute execution
deadline and a separate 6-minute cleanup deadline.

Stop the **v2 test daemon** before this test (the singleton lock prevents another
controller). Ensure v1/other writers are inactive and Lume is healthy with no
running VMs. Do not stop unrelated VMs automatically. Use a new persistent state
directory: the journal is retained if an ambiguous result needs inspection.

```sh
VIRFIELD_LIVE_MUTATIONS=I_APPROVE_TEMPORARY_VM_DELETION \
VIRFIELD_LIVE_STATE_DIR=/absolute/path/to/new-live-acceptance-state \
VIRFIELD_LIVE_TEMPLATE=macos-27-golden \
go test ./internal/control -run '^TestLiveLifecycle$' -count=1 -v -timeout=26m
```

The environment variable is a deliberate guard, not a substitute for obtaining
user authorization. Do not run it merely to make CI green. Unknown clone outcomes
remain quarantined and are never force-deleted by the test.
