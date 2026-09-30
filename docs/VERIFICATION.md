# Verification

This is the current acceptance record and test runbook. It distinguishes the
verified local host from the still-unimplemented Balda/Broker deployment. Results
were collected on Apple Silicon macOS on 2026-09-29 and 2026-09-30 (timestamps UTC),
using Go 1.26.8, patched Lume 0.5.3 and macOS guest build `26A428`.

## Accepted behavior

| Check | Result and evidence |
|---|---|
| Real Apple IPSW download | 26,626,436,228 bytes downloaded from Apple; two daemon restarts exercised HTTP Range resume; pinned SHA-256 passed |
| Clean full-profile image build | `TestLiveImageRebuild`: PASS, 1277.65 s; no manual recovery or mutation retry during this build |
| Full-profile clone execution | `TestLiveLeaseSSH`: PASS, 88.32 s; two clones executed Swift and produced real screenshots |
| Admission and API responsiveness | Third lease refused at 2/2; idempotency passed; maximum API read latency during the clean build was 17.1005 ms |
| SSH isolation and tunnels | Own keys accepted; cross-lease and inherited image keys rejected against each clone's new host pin; tunnel cleanup passed |
| Daemon crash/restart | Real installed-service restart preserved leases; a crash during dispatched SSH preparation quarantined the lease without replay; explicit cleanup passed |
| MCP and browser | Installed HTTP/stdio discovery and status passed; anonymous MCP rejected; desktop/390px console passed with no browser errors |
| Backup integrity | Isolated snapshot copy passed SQLite integrity/foreign keys/schema checks and exact config/token/image-identity comparison |
| Host state at acceptance | Only the main `macos27` template; 0/2 slots used, no unresolved jobs; temporary image/clone credentials and listeners removed |

The accepted clean image was `image-93604e81b7b84976aa96a86173072f2d`, created by
job `job-da632cf1a2dffeacb8076ccde6e4fd38`. It exercised verified cached media,
fresh installation, unattended setup, remaining Assistant screens, password
rotation, Recovery/SIP, Xcode 27.2 beta (27B5019j), guest tools, Gatekeeper, AMFI,
TCC and sudo. Reboot checks covered password persistence, key-only SSH, Finder,
Swift execution, System Events and actual Peekaboo permissions. Publication
required final confirmed guest shutdown.

The expanded clone run used `lease-23a7ccc51bce7bcf74a1f7c931fb7e36` and
`lease-e222d3c84415c1d221a2ad6f9f74819c`. Both clones and their private identities
were cleaned up. Temporary golden cleanup job
`job-1a29313d808fda7023977a9c42f63d38` succeeded. The original golden had also been
explicitly deleted and rebuilt through the API earlier in acceptance; the current
`macos-27-golden` is verified and stopped.

The two Lume dependency fixes are required: remove a completed guest from the
running cache, and request clean shutdown after successful unattended setup.
The latter eliminated the observed fresh-account password failure in the clean
rerun. The focused cache-release and setup-shutdown regressions passed; applying
the versioned patch with zero fuzz to the pinned archive reproduces the built
Swift sources. Earlier failed development attempts are not counted as passes.

The accepted runtime build is commit `44a1c15` in the deployment's `release.json`.
Repository/documentation cleanup after that acceptance does not itself upgrade
the installed service. The live deployment remains outside the checkout at
`~/.virfield-v2`; its `release.json` records installed binary hashes and final
checks. Snapshot `backup-caac74afb3773d106eb2f95840c6e2c9` passed the isolated
integrity/identity rehearsal. VM disk restore was not part of that check.

Private development evidence is archived outside the worktree under
`~/.virfield-v2/archives/repository-cleanup-20260930/development-state/`.
The `v2-live-20260929-a/fresh-profile-20260930/` directory contains verification
manifests and an acceptance summary without copied credentials. These private
artifacts are not required to build the repository.

The three unrelated VMs (`macos-15-golden`, `pdf-hud-macos27`,
`uitest-26.4.1-golden`) remained stopped and untouched. Host SIP and Gatekeeper
remain enabled. The previous service and MCP processes were stopped; their
source and build artifacts are now absent from the current worktree. The
previous application is available only by checking out branch `v1`.

## Automated checks

Run from the repository root:

```sh
make check
make build
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --redact --log-opts=--all
```

`make check` includes documentation links, Python Assistant/Recovery regressions,
deployment path/port/permission checks, gofmt, `go vet` and `go test -race ./...`. Live tests skip without explicit opt-in.
Fixture tests need permission to bind loopback sockets. Tests cover concurrent
admission, external/unknown capacity, journal rollback/reopen, ambiguous effects,
delayed starts, expiry, cleanup/drift, authentication, strict input, SDK tools,
redirect credential protection, downloads, host-key pinning and bounded I/O.

The prior `govulncheck` v1.6.0 run found zero reachable or imported-package
vulnerabilities. One advisory affected an unimported OpenPGP package; that result
is dated acceptance evidence, not a claim about the current vulnerability feed.
Linux cross-compilation passed. Lume-specific regression commands live with its
[dependency recipe](IMAGE-PIPELINE.md#profile-and-dependencies).

## Read-only installed-service checks

```sh
VIRFIELD_LIVE_LUME_URL=http://127.0.0.1:7777 go test ./internal/lume -run '^TestLiveReadOnly$' -count=1 -v
VIRFIELD_LIVE_MCP=1 \
VIRFIELD_LIVE_TOKEN_FILE="$VIRFIELD_HOME/token" \
VIRFIELD_LIVE_MCP_BINARY="$VIRFIELD_HOME/bin/virfield-mcp" \
go test ./internal/mcpadapter -run '^TestLiveInstalledMCP$' -count=1 -v
```

Set `VIRFIELD_HOME` to the existing deployment as described in
[Operations](OPERATIONS.md#paths-and-configuration). These checks do not establish
image or clone correctness on their own.

## Live mutation checks

The opt-in values below acknowledge VM creation and permanent deletion; obtain
operator authorization for the exact target before setting them. Do not run
these tests merely to make CI green. Require healthy idle inventory, no running
VMs, and no unresolved jobs. Never stop unrelated VMs to satisfy prerequisites.

The clean image rebuild command and destructive scope are maintained only in
[Image pipeline](IMAGE-PIPELINE.md#reproducible-live-acceptance).
For two disposable clones of an already verified image:

```sh
VIRFIELD_LIVE_LEASE_SSH=I_APPROVE_TEMPORARY_VM_DELETION \
VIRFIELD_LIVE_TEMPLATE_ID=macos27 \
VIRFIELD_LIVE_TOKEN_FILE="$VIRFIELD_HOME/token" \
VIRFIELD_LIVE_STATE_DIR="$VIRFIELD_HOME" \
go test ./internal/client -run '^TestLiveLeaseSSH$' -count=1 -v -timeout=17m
```

This uses the deployed API and preserves the golden. The full tool probes run
when the selected template configures `uitest-27-v1`. Unknown outcomes remain
quarantined for inspection, not forcibly removed.

For the lower-level controller lifecycle test, first stop the manager (the host
singleton lock permits only one controller). Use a new private state directory:

```sh
VIRFIELD_LIVE_MUTATIONS=I_APPROVE_TEMPORARY_VM_DELETION \
VIRFIELD_LIVE_STATE_DIR=/absolute/private/new-acceptance-state \
VIRFIELD_LIVE_TEMPLATE=macos-27-golden \
go test ./internal/control -run '^TestLiveLifecycle$' -count=1 -v -timeout=26m
```

## Not yet accepted

- Remote CI execution, remaining VM-mutation crash points, prolonged soak and
  VM-disk disaster recovery. Unit/mock checks and state snapshots do not replace
  these exercises.
- Registry pull and macOS builds other than `26A428`.
- Container-facing routing/TLS and per-principal authentication.
- Broker, Runner, Callee/provider installation, Balda and Prism integration.
  The complete task → VM → execution → result → cleanup workflow from Balda
  has not passed end-to-end acceptance.

## Release procedure

After checks pass on a clean committed tree, build an archive with
`python3 tools/release.py v2.0.0 --output /absolute/new/release-directory`.
The packager uses only committed source and freshly built macOS/arm64 binaries;
it includes dependency notices, binary hashes and the exact Git revision. It
refuses dirty source, a mismatching existing version tag or existing outputs.
Inspect/extract the archive, scan it for secrets, verify its checksum and smoke-test
CLI initialization and service preparation outside the original checkout.

Push only the intended branches; require successful GitHub Actions checks for the
exact main commit before creating the release tag and publishing the archive plus
`SHA256SUMS`. Never include private deployment state or local acceptance logs.
