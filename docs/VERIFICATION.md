# Verification

This is the current acceptance record and test runbook. It distinguishes the
verified local host from the still-unimplemented Balda/Broker deployment. Results
were collected on Apple Silicon macOS through 2026-10-01 (timestamps UTC),
using Go 1.26.8 and patched Lume 0.5.3. The full UI-test suite is verified on
macOS 27 build `26A428`; versioned image coverage is recorded separately below.

## Pending release gates

The versioned-image candidate is **not production accepted**. Before replacing
the deployed release or registering it in Codex and Claude Code, complete:

1. An unencrypted golden and successful ordinary clone boot for the requested
   Monterey/Xcode combination; the known blocker is recorded below.
2. A fresh full image/security/Xcode workflow, pinned SSH workload, artifact
   export and confirmed cleanup, with exact versions and evidence recorded here.
3. GHCR import and portable publish → pull-back → ordinary clone → workload
   acceptance, including credentials, exact digest and temporary-VM cleanup.
4. Install the accepted build, register `virfield` in both clients using
   [Operations](OPERATIONS.md#register-codex-and-claude-code), and validate the
   [agent workflow](AGENT-GUIDE.md#client-registration-and-acceptance) in each.

The agent guide and operations/image/verification runbooks are embedded in MCP
as `virfield_help` and four read-only resources. Automated protocol tests verify
help without a daemon, resource/help equality, startup instructions, topic
allowlisting and documentation coverage for every exposed tool. This does not
claim that the new tools are installed or that either client has passed live
workflow acceptance. The release remains pending until the client checks pass.

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

The full-profile acceptance above was collected on commit `44a1c15`.
As checked on 2026-10-01, the installed runtime is candidate `9090337`, with
`release_status: registry_candidate_pending_live_acceptance` and schema 6.
The disk gate, 17 MCP tools and embedded agent runbooks are deployed. Installed
HTTP/stdio discovery, authenticated status and anonymous rejection passed.
The live deployment remains outside the checkout at `~/.virfield-v2`;
its `release.json` records the installed commit and binary hashes. The explicitly
authorized schema 5 → 6 update followed integrity and foreign-key checks of
snapshot `backup-ff4570a85e703b595b92a9c2606cf3f3`. VM disk restore was not part of
that check. Subsequent checkout commits are not installed until confirmed by
the deployment manifest; the candidate is not production accepted.

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

The candidate stdio adapter also passed a read-only smoke check from a working
directory outside the checkout: 13 tools, four documentation resources, matching
help/resource content, server instructions and authenticated daemon status.
This is a binary/protocol check, not Codex or Claude workflow acceptance.

## GHCR candidate checks (2026-10-01)

The working candidate has 17 MCP tools, including source discovery, manifest
resolution, import and portable publication. `make check` (including race tests),
`make build` and staticcheck passed. Tests exercise legacy/OCI formats, digest
mismatch, redirect refusal, credential-file permissions, bounded metadata,
imported VM resource/network normalization, durable import replay, portable
rebuild/source protection, interrupted/ambiguous upload and cleanup ordering.
These fixture results do not establish live guest or registry round-trip success.

A read-only public GHCR probe resolved
`ghcr.io/trycua/macos-sequoia-vanilla:latest` to
`sha256:781469ea44dd687f5cfbb130131ea2efee799e34b0fe2a297a0ecab013427266`
with 17,818,752,054 declared compressed bytes. No disk was pulled and no tag was
published in this check. Reproduce with:

```sh
VIRFIELD_LIVE_REGISTRY_METADATA=1 go test ./internal/registry -run '^TestLivePublicManifest$' -count=1 -v
```

The deployed candidate imported `macos-sequoia-vanilla:15.2` at that same digest
(job `job-81880fd34056b9c0186af4b3624b4865`). Download and digest validation passed.
Both System and Data reported FileVault=false, Encryption=false, Locked=false.
The job stopped at credential rotation (`sysadminctl -14915`) after offline setup
replaced the imported native account record. It did not publish a ready golden.
The fix uses SSH bootstrap for imported accounts and reserves offline setup for
fresh IPSW installations; a clean import rerun is required to establish the fix.
Full import and publish/pull-back acceptance and client workflow checks remain
release gates. Publication rebuilds a portable recipe;
raw source-disk export and preservation of manual golden changes are not offered.

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

## Versioned images (2026-10-01)

| Guest / policy | Evidence |
| --- | --- |
| Monterey 12.6 / 21G115, default protection, no Xcode | IPSW download and native Assistant passed; source boot, key-only SSH, password rotation and Finder passed. **Not accepted:** both ordinary clones stalled before SSH; FileVault is off but System/Data report encryption at rest. Both test clones and the failed encrypted golden were deleted. |
| Monterey with `automation` security | Implementation present; live acceptance blocked by the unencrypted golden requirement |
| Xcode 13.4.1 / 13F100 | Catalog resolution and compatibility checks pass; live install awaits an authenticated Apple archive |
| Other catalog macOS generations | Selectable; not yet a claim of live acceptance on this host |

The disk-policy probe was run in the real Monterey guest: both boot volumes
reported `FileVault=false`, `Encryption=true`, `Locked=false`. The new pipeline
rejects that state before completing setup and again before publication. The
installed runtime has not yet been upgraded with that gate, and the failed golden was deleted through the API (job
`job-d8a05d245b9ca804b35e0212a9be87fe`, succeeded; absence confirmed in Lume).
The registered recipe remains; there is no accepted Monterey golden.
No clone-identity workaround is included. A fresh successful clone acceptance
run and disk-policy validation remain required before release.

The initial Monterey bring-up required inspected operator recovery while native
UI and older OpenSSH compatibility were being fixed. It is not a clean-run
acceptance claim. The finalized pipeline must be exercised from a fresh image
before recording that result. Older OpenSSH needs both
`KbdInteractiveAuthentication no` and `ChallengeResponseAuthentication no`;
checking just `PasswordAuthentication` does not establish key-only access.

## Not yet accepted

- Remaining VM-mutation crash points, prolonged soak and VM-disk disaster recovery. Unit/mock checks and state snapshots do not replace
  these exercises.
- Registry pull, portable publication/pull-back and macOS builds not explicitly recorded in the versioned-image matrix.
- Container-facing routing/TLS and per-principal authentication.
- Broker, Runner, Callee/provider installation, Balda and Prism integration.
  The complete task → VM → execution → result → cleanup workflow from Balda
  has not passed end-to-end acceptance.

## Portable release checks (2026-09-30)

The source archive passed `make check`, `make build` and `go mod verify` with an
empty HOME and separate empty Go module/build caches. A fresh Python 3.14 VNC
environment installed the pinned requirements and passed imports and `pip check`.
Deployment tests cover a different account/HOME, paths with spaces, custom ports,
insecure token refusal and missing tools. Archive smoke checks also exercised
initialization, plist/MCP generation, refusal to overwrite and the Lume wrapper's
configured port, without starting a real VM service.

Gitleaks 8.30.1 found no secrets in the complete reachable Git history or extracted
release contents. An additional local comparison against current deployment
credentials found no matching Git blobs. Go vulnerability checks found no
reachable or imported-package vulnerabilities. Archive checks verified binary
hashes, ad-hoc signatures and absence of the author's HOME path in all four
binaries. Secret scanning is evidence, not a guarantee; `.gitignore` and the CI
scan guard future changes as well.

GitHub [Checks](https://github.com/mallexxx/virfield/actions/workflows/check.yml)
runs the Linux/macOS matrix; publication requires both jobs to pass for the exact
release commit. This portability check does not claim a full VM image build on
a second physical Mac. The image acceptance above remains tied to its stated
hardware/OS/tool versions.

## Release procedure

Using Python 3.12 or newer, after checks pass on a clean committed tree, build an archive with
`python3 tools/release.py v2.0.0 --output /absolute/new/release-directory`.
The packager uses only committed source and freshly built macOS/arm64 binaries;
it includes dependency notices, binary hashes and the exact Git revision. It
refuses dirty source, a mismatching existing version tag or existing outputs.
Inspect/extract the archive, scan it for secrets, verify its checksum and smoke-test
CLI initialization and service preparation outside the original checkout.

Push only the intended branches; require successful GitHub Actions checks for the
exact main commit before creating the release tag and publishing the archive plus
`SHA256SUMS`. Never include private deployment state or local acceptance logs.
