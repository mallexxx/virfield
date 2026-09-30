# Architecture

Virfield is a single Go module at the repository root. Clients use the daemon API;
only the controller and its adapters own VM lifecycle effects. Broker execution
policy and Runner/Callee workflow execution belong to separate applications.

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
production operation requires that other writers do not manage these VMs.
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
skipping readiness, resource and provisioning contracts. Existing leases without an SSH identity require
release and reacquisition; the existing verified image remains usable.

## API boundary

`internal/httpapi` implements the bearer-authenticated `/api/v1` contract.
`internal/domain` owns request/response models; `internal/client` is the shared
Go API client. `/mcp` wraps that API with the official MCP SDK. The embedded
console and stdio adapter never access Lume or SQLite directly.

Mutations return durable job/lease identities before background execution.
Preparation and cleanup use explicit dispatch checkpoints. Idempotency keys
identify a request; they are not permission to replay an uncertain external
side effect. Unknown outcomes retain reservations for operator inspection.
There is no arbitrary host-shell API, generic guest-script endpoint or access to
personal SSH credentials. Operational commands are documented in
[Operations](OPERATIONS.md); image mutation contracts are in
[Image pipeline](IMAGE-PIPELINE.md).

The `/api/v1` route prefix, SQLite schema versions and `uitest-27-v1` image recipe
are current protocol identifiers. They are independent of the retired
application's Git branch and must not be renamed as cosmetic cleanup.

## Sources and implementation rules

- [Effective Go](https://go.dev/doc/effective_go) and [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).
- [Lume CLI reference, documented for 0.5.3](https://cua.ai/docs/reference/lume/cli-reference).
- [Serve the Lume API](https://cua.ai/docs/how-to-guides/lume/serve-api).
- [Lume API snapshot](reference/lume-0.5.3-api.json): output of the installed `lume dump-docs --type api --pretty`.

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
