# Architecture

Virfield is one modular Go application. Clients call `/api/v1`; the controller
and runtime adapters alone perform infrastructure effects. The maintained
candidate is on `codex/retire-lume-release-hardening`; `main` is not promoted.
Broker/Runner own execution semantics.

## Modules

| Module | Current responsibility |
|---|---|
| `cmd/virfieldd` | Config, single-owner locks, runtime wiring, API and shutdown |
| `internal/control` | Lease/image admission, durable phases, reconciliation, expiry and cleanup |
| `internal/store` | SQLite schema 8, atomic lease/job/event/request/template records, snapshots and retention; pre-migration snapshot protects older transfer-unaware executors |
| `internal/provider`, `internal/fleet` | Runtime seam and local Tart + SSH-node routing |
| `internal/tart`, `internal/tartimage` | Tart inventory/lifecycle and pinned Packer image adoption |
| `internal/baremetal`, `internal/nodeimage`, `internal/wol` | SSH physical nodes, node VM overlays, securing goldens and wake packets |
| `internal/guestssh`, `internal/qualify` | Account-specific credentials, host pins and guest qualification |
| `internal/catalog`, `internal/registry` | Version metadata and allowlisted registry metadata |
| `internal/httpapi`, `internal/web` | Bearer-authenticated API and embedded local console |
| `internal/client`, `internal/mcpadapter` | API-only CLI/SDK and MCP adapters with embedded documentation |
| `internal/appleauth`, `apple-browser` | Scoped native Apple website authentication |
| `internal/orchard` | Unaccepted Orchard candidate adapter |

The Lume backend, image factory, wrapper and build/install files are removed.
Legacy profile fields are read only for journal compatibility; Tart rejects old
IPSW recipes before reserving a build. The retired host service is independent
of the candidate and tracked in [Verification](VERIFICATION.md).

## Capacity and recovery

Acceptance commits reservations before returning. Capacity includes active
observed targets and unreleased durable reservations without double-counting.
Local Tart slots and secondary node slots are separate. Primary image builds and
portable exports reserve CPU/RAM/disk before dispatch, in the same budget as
workers; stopped ready goldens are excluded from active reservations. An unreachable node
remains visible; WoL-capable nodes may retain cached image inventory for waking.
A cached image is not proof of a currently reachable guest.

Unknown/stale primary inventory blocks admission. Unknown mutation outcomes keep
reservations and require operator inspection; a dispatch is never blindly
replayed. Stop and deletion are distinct effects. Capacity is freed only after
fresh absence is confirmed. Expiry revokes access even during backend failure.

Local Tart/node VM readiness requires running state, address, per-lease credential
isolation and authentication against a pinned host key. Physical host leases add
one caller key to the configured account and reserve its workspace. They do not
reset the host or kill workloads under the shared account. A physical host has one exclusive reservation independent of its VM slots.
Shared sleep holds are reference-counted; foreign holds are retained. VM cleanup
requires confirmed container removal before deleting overlays or releasing holds.

The approved VM account policy is standard `admin/admin`, without automatic
random account-password rotation. Standard-account candidates (`0f8bc20` and
later) implement this policy. Old golden credentials remain
unchanged; a new disposable clone can convert its known old account once. Distinct SSH keys and host
pins remain required; a standard console password is not a tenant-isolation
boundary. Existing keychain items, workspaces and goldens must be preserved.

Transient unverified inventory preserves the last verified ready endpoint and
existing streams. Confirmed `vm_drift` instead moves a lease to `needs_attention`;
the existing operator resolve only queues cleanup. Recovery-in-place and durable
drift/tunnel-close evidence are [planned work](VIRFIELD-3-PLAN.md#operator-feedback-backlog),
along with complete resource refusal arithmetic and verified local golden transfer.

## API boundary

REST scoped principals support `lease:own` and `image:build`; ownership checks
protect lease access. HTTP MCP currently uses the operator principal. These are
not v3 tenant grants. Loopback-only API deployment uses an explicit Host allowlist;
remote use requires an authenticated TLS proxy and accepted routing.

Clients never access SQLite or runtime storage. Do not add arbitrary host shell
or generic workload execution through this API. `/api/v1`, schema versions and
recipe identifiers are protocol contracts, not product marketing versions.

## Development rules

Use bounded contexts, safe error codes, atomic persist-before-effect transitions,
strict SSH host checking and private credential files. Never hold admission locks
across network effects. Run formatting, vet, race tests and release checks; test
failure behavior and provider routing. [Operations](OPERATIONS.md) owns commands;
[the 3.0 plan](VIRFIELD-3-PLAN.md) owns future architecture.
