# Development

Virfield is a modular Go application. Clients call `/api/v1`; only the controller
and runtime adapters perform infrastructure effects. Execution services own
workload commands, queues and artifacts.

## Find the code

| Module | Responsibility |
| --- | --- |
| `cmd/virfieldd` | Configuration, single-owner locks, API and shutdown |
| `internal/control` | Admission, durable phases, reconciliation, expiry and cleanup |
| `internal/store` | SQLite records, atomic transitions, snapshots and retention |
| `internal/provider`, `internal/fleet` | Runtime contracts and routing |
| `internal/tart`, `internal/baremetal` | Local Tart and SSH physical/node-VM effects |
| `internal/tartimage`, `internal/nodeimage` | Image construction and qualification |
| `internal/guestssh`, `internal/guestkeychain`, `internal/qualify` | Guest identity, policy and readiness |
| `internal/httpapi` | Authenticated API and embedded console |
| `internal/client`, `internal/mcpadapter` | API clients, CLI helpers and MCP |
| `internal/appleauth`, `apple-browser` | Scoped native Apple website sign-in |
| `internal/orchard` | Experimental Orchard adapter |

## Preserve lifecycle rules

Persist the reservation, request key and phase before effects. Keep network work
outside admission locks. Capacity includes observed targets and unreleased
reservations without double-counting; image builds share worker CPU/RAM/disk
budgets. Unknown inventory blocks admission. Unknown dispatched outcomes require
inspection rather than blind replay.

Readiness requires a verified runtime/image identity, host pin, caller-key
isolation and native guest checks. Temporary unverified inventory preserves the
last verified ready endpoint and existing streams while pausing new connections.
Confirmed drift revokes access; revalidation checks the original VM without
preparing it or rotating keys. Expiry revokes access even during backend failure.
Cleanup frees capacity only after fresh absence is confirmed.

Keep credentials and diagnostic payloads private. Use bounded contexts, safe
error codes and atomic state transitions. Add regression coverage for failure
behavior, concurrency and provider routing. Runtime state must never enter Git.

## Check and package

```sh
make check
make build
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --redact --log-opts=--all
```

Build releases on macOS with Python 3.12 or newer, from a clean committed tree:

```sh
python3 tools/release.py vX.Y.Z --output /absolute/new/release-directory
```

Choose a real version. Packaging includes committed source, macOS/arm64 binaries,
dependency notices in `_THIRD_PARTY_NOTICES`, `release.json` and `SHA256SUMS`;
it refuses conflicting tags
or existing outputs. Verify checksums, extract into a clean directory and run
checks there before distribution. Configure signing for your deployment separately.
See [Operations](OPERATIONS.md#upgrade-and-restore) for installation and rollback.
