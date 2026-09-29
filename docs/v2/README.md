# Virfield v2 — control-plane implementation

**Branch:** `codex/virfield-v2`. **Status:** local Go replacement installed; system-service SSH, tunnels and restart acceptance passed; full UI-test image provisioning remains gated on guest-policy authorization.

This implements the first vertical slice of §13/§16 stage 2 in the
2026-09-29 Balda/Callee/Prism plan. Broker, Runner and Balda integration remain
separate applications. The TypeScript application and existing VM data are
preserved as migration references. On the acceptance host, v1 service and MCP were replaced after live validation; simply building v2 does not perform that migration.

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

The initial configuration is a skeleton. Add the pinned image profile and image
tool paths from [Image Manager](IMAGE-PIPELINE.md), then build and verify the image
through v2 before acquiring leases. An unverified existing golden is refused.

In another terminal:

```sh
./bin/virfield -token-file "$PWD/.v2-state/token" status
```

Open `http://127.0.0.1:7780` and enter the token from `.v2-state/token`.
The page keeps it in memory only. `status` and the console read SQLite plus the
last reconciler observation; polling clients do not repeatedly call Lume.

### Acquire, inspect and renew

```sh
./bin/virfield keygen "$PWD/.v2-state/story-42"
./bin/virfield -token-file "$PWD/.v2-state/token" -key story-42-attempt-1 \
  acquire macos27 3600 "$PWD/.v2-state/story-42/id_ed25519.pub"
./bin/virfield -token-file "$PWD/.v2-state/token" lease LEASE_ID
./bin/virfield -token-file "$PWD/.v2-state/token" job JOB_ID
./bin/virfield -token-file "$PWD/.v2-state/token" events 0
./bin/virfield -token-file "$PWD/.v2-state/token" renew LEASE_ID 2026-09-29T18:00:00Z
```

Each lease requires a fresh plain Ed25519 public key. The CLI generates its private
key locally in a new 0700 directory; it never uploads it. MCP/UI accept only the
public key. A key already used by an active lease returns `ssh_key_in_use`.
The same key and idempotency key remain valid for retries of the same acquisition.

After the lease reaches `ready`, export a connection bound to its host key:

```sh
./bin/virfield -token-file "$PWD/.v2-state/token" ssh-config LEASE_ID "$PWD/.v2-state/story-42"
ssh -F "$PWD/.v2-state/story-42/config" virfield
```

The export verifies that the local private key belongs to the lease, pins its
Ed25519 host key, disables agent/password authentication and connection sharing,
and refuses to overwrite existing files. These local identity files belong to
the caller; remove them when no longer needed. Direct guest IP routing is supported. `tunnel LEASE_ID` opens a loopback SSH forwarding endpoint; `tunnel-close LEASE_ID` closes it. Authenticate using the same lease key and pinned guest host key. Tunnels expire with the lease and close on release, drift or daemon shutdown; reopen them after restart. Container routing remains a separate integration.

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
| `internal/guestssh` | Bounded pinned SSH, image bootstrap, per-lease credential isolation |
| `internal/images` | Pinned IPSW downloads, versioned guest provisioning, image verification |
| `internal/store` | SQLite schema, atomic state + event transactions, idempotency |
| `internal/lume` | Sole Lume HTTP/CLI adapter, fixed image commands, no blind mutation retries |
| `internal/httpapi`, `internal/web` | Versioned authenticated API, embedded console |
| `internal/client`, `cmd/virfield` | API-only CLI |
| `internal/mcpadapter`, `cmd/virfield-mcp` | Official Go SDK stdio and authenticated HTTP MCP adapter |
| `internal/hostresources`, `internal/logging` | CPU/RAM/storage admission and bounded service logs |
| `cmd/virfield-lume` | Independent launchd entrypoint with bounded Lume logs |
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

`ready` requires a running VM, valid guest IP and successful authentication
against its new host key after per-lease credential isolation. Each clone receives
a distinct administrator password, daemon management key and Ed25519 host key.
Its `authorized_keys` contains only its management key and caller public key;
the image key is explicitly tested for authentication rejection. Effective sshd
policy must disable password, keyboard-interactive and root login, and agent
forwarding. Private material stays in owner-only daemon files; API/MCP expose only
connection metadata and public keys.

An interrupted SSH isolation is never replayed. The lease becomes
`needs_attention`, remains reserved and can be released or expire normally:
its clone ownership and completed start are already known. An IP change after
verification requires a new lease. Schema version 4 prevents older binaries from
skipping readiness, resource and provisioning contracts. Existing v2 leases without an SSH identity require
release and reacquisition; the existing verified image remains usable.

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
`virfield_job`, `virfield_events`, `image_build`, `vm_tunnel`, `vm_tunnel_close`. A full pool returns `capacity_exhausted` with
an actionable message. **Queueing belongs to Broker**, not this host daemon.

The daemon currently binds only to loopback. Container/Broker access needs an
explicit TLS reverse proxy and network policy. The client rejects remote
cleartext HTTP and never follows redirects with its bearer token. HTTP MCP is served at `/mcp` using the same owner bearer token. Per-principal credentials are not implemented.

## launchd

Use a **system LaunchDaemon running as the VM-owning user**, not a GUI
LaunchAgent. On current macOS, Local Network Privacy can deny guest SSH from
an agent even while identical terminal code works. Apple's [TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy)
documents automatic local-network access for launchd daemons. Do not change host
TCC or run VM workers as root to work around this.

`deploy/ai.virfield.virfieldd.plist.example` has explicit UserName, GroupName, HOME
and PATH placeholders. Lume is a separate system job through `virfield-lume`;
the controller never restarts it. Both services rotate logs at 10 MiB with three
archives. Token permissions are 0600; state and credentials directories are 0700.
Startup stderr remains a separate diagnostic log. System launchd registration
requires macOS administrator authorization.

The scripts in `deploy/install-local.py`, `install-launchdaemons.sh` and
`switch-mcp.py` are the reviewed **admin/UID 501 host migration**, not a portable
installer. Stage an idle consistent snapshot, install jobs, accept SSH through the
system service, then remove the exact Eagle-managed v1 entry and switch MCP.
Do not restart the Eagle Dashboard or unrelated services. Keep the private
migration backup; never point v1 at the v2 database.

### Quotas and backups

`resource_limits` configures CPU count, RAM bytes and reserved host disk bytes;
`storage_paths` maps Lume locations to absolute paths. Defaults reserve 25% of
host CPU/RAM and 20 GiB free disk. Admission accounts for active external VMs and
reserved leases. Unknown resources fail closed. Disk admission conservatively
reserves full virtual disk growth, even for sparse/APFS-cloned disks.

`virfield -token-file /absolute/state/token backup` requires an idle healthy pool
and no unresolved jobs. It snapshots SQLite, config, token and active image
identities into a new private directory under `backups/`, checks integrity, and
keeps five completed snapshots. Failed partial copies are removed. **VM disks
and the IPSW cache are not included.** These need a separate storage backup.

To restore: stop the daemon, preserve the current directory, copy a complete
snapshot into a new private state directory, adjust `state_dir` and `token_file`
to that directory, and verify the corresponding stopped Lume image disks still
exist. Start exactly one daemon with that configuration. Never restore old
credentials against a VM whose SSH identity changed after the backup.

## Verification

```sh
make check                       # formatting, vet, race tests; no live VM mutations
make build                       # four Go binaries
VIRFIELD_LIVE_LUME_URL=http://127.0.0.1:7777 go test ./internal/lume -run TestLiveReadOnly -v
```

The test suite covers concurrent admission, external/unknown slots, idempotency,
SQLite rollback/reopen, clone quarantine, restart recovery, delayed starts,
expiry, cleanup failure, drift, token/origin checks, strict JSON, SDK tool calls,
redirect credential protection and Lume HTTP request contracts. The live
read-only check validates only host status and inventory.

**Live lifecycle accepted:** two real clones reached Lume SSH readiness, a third
request was refused, SQLite reopen preserved leases, and both clones were deleted.
**Scoped SSH accepted:** two real clones rejected each other’s keys and the image
key; own keys and exported OpenSSH configs worked after a hard daemon restart.
Private-secret API checks, capacity refusal, idempotency and cleanup passed.
**Base-image acceptance passed:** a real Apple download with resume and SHA-256,
followed by a clean cached-media rebuild through Assistant, Recovery/SIP, SSH
rotation and reboot verification; two clones and cleanup passed in 10m38s.
See [the evidence and limitations](VERIFICATION.md).
**Not yet accepted:** daemon crash injection during actual VM mutations and long-duration stability. SQLite full-disk rollback and backup integrity/retention are covered separately. Automated mock tests do not establish these claims.

## Remaining planned modules and release gates

| Planned scope | Current state / release gate |
|---|---|
| Core lifecycle, API, CLI, stdio MCP, minimal UI | Implemented; two-VM live lifecycle passed |
| Image Manager: download/pull/build/promote | Pinned download/build/verify/promotion implemented; registry pull pending |
| Versioned provisioning jobs | Base pipeline accepted; versioned Xcode/tools/Gatekeeper/AMFI/TCC recipe implemented, live application awaits explicit guest-policy authorization |
| Scoped SSH credentials, verified guest login, tunnels | Per-lease password, key and host-key isolation plus caller-owned keys implemented and live-tested; loopback SSH tunnels and installed-service crash/restart acceptance passed |
| Resource quotas beyond two VM slots | CPU/RAM/disk admission implemented and tested; installed host quotas configured |
| HTTP MCP, Broker-facing deployment | Authenticated HTTP MCP implemented and live-tested; TLS/container routing and scoped principals pending |
| Production operations | Backup/restore integrity rehearsal, bounded logs, system launchd and v1 cutover accepted; prolonged soak and remaining live mutation fault injection pending |
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
