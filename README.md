# Virfield

Virfield v2 is a Go control plane for disposable macOS VMs on an Apple Silicon
host. It builds and verifies golden images, leases isolated clones, enforces a
maximum of two macOS VMs, exposes authenticated HTTP/MCP APIs, and cleans up
expired or released leases.

`main` contains only this implementation. The previous application is preserved
in the Git branch `v1`; it is not part of the current build or deployment.

## Start here

| Document | Owns |
|---|---|
| [Agent guide](docs/AGENT-GUIDE.md) | MCP workflows, tool arguments, pinned SSH, artifacts, retries and errors; embedded as `virfield_help` |
| [Operations](docs/OPERATIONS.md) | Installation, paths, configuration, CLI/MCP, service operation, recovery and backups |
| [Image pipeline](docs/IMAGE-PIPELINE.md) | Supported image profile, dependencies, build stages, guest policy and image recovery |
| [VS Code remote debugging](docs/REMOTE-DEBUG.md) | Host SwiftPM build/test, artifact sync and remote `debugserver` |
| [Architecture](docs/ARCHITECTURE.md) | Modules, API boundary, lifecycle invariants and development rules |
| [Verification](docs/VERIFICATION.md) | Acceptance evidence, reproducible checks and unverified/deferred scope |

These documents describe the current implementation. Installation instructions
belong in Operations; acceptance claims belong in Verification. Do not maintain
separate version-specific README copies or a second set of setup instructions.

## Install

Download the macOS/arm64 archive and checksums from
[GitHub Releases](https://github.com/mallexxx/virfield/releases), or build below.
Follow [Operations](docs/OPERATIONS.md#prerequisites) for host requirements and
installation. A different username, HOME or private deployment directory is
supported; no personal checkout paths are required.

## Build

Go builds the control plane; on macOS, Swift also builds a native WebKit browser
for Apple Developer sign-in. The older
[apple-auth/Package.resolved](apple-auth/Package.resolved) helper is retained in
source but is not built or exposed by the sign-in page.
The Go minimum version and dependencies are pinned in [go.mod](go.mod). No Node.js, npm, React or frontend build is needed;
the console is embedded from [internal/web](internal/web).

```sh
make check
make build
./bin/virfield -help
```

`make check` runs documentation link checks, Python image-driver/deployment tests, gofmt,
Go vet and race tests. It does not create or delete real VMs. The host image
pipeline additionally needs the patched Lume dependency, Python/VNC tools,
Tesseract and, for the UI-test profile, a compatible Xcode bundle; see
[Image dependencies](docs/IMAGE-PIPELINE.md#profile-and-dependencies).

## Repository layout

```text
cmd/          virfieldd, virfield, virfield-mcp, virfield-lume entrypoints
apple-auth/   local Apple Developer sign-in helper for Xcode downloads
internal/     Go implementation, embedded console and guest automation
deploy/      pinned Lume patch/build recipe and deployment examples
docs/        operational documentation and acceptance evidence
tools/       repository maintenance checks
```

Runtime state, VM disks, credentials, downloaded media and deployment binaries
belong outside the source checkout. `bin/` contains ignored local build outputs.
The Go module stays at the repository root; there is no nested v2 application.

## Integration boundary

Virfield manages the host VM lifecycle. Broker owns execution queues and durable
runs; Runner/Callee executes work inside the VM. The flow is
`Balda → Broker → Virfield → VM/Runner`. Broker image tools live in the
separate `execution-broker` repository. Container ingress still needs live
acceptance on the target host.
See [current verification limits](docs/VERIFICATION.md#not-yet-accepted).

Create a golden by version with MCP `image_catalog` / `image_create`, or CLI
`image-create NEW_ID 15.2 16.2`. Versions are resolved from catalogs and
pinned in the journal; they are not limited to preconfigured templates. See
[image version selection and Xcode authentication](docs/IMAGE-PIPELINE.md#choose-macos-and-xcode-versions).

Use MCP `security: "automation"` (CLI `-security automation`) to apply guest SIP,
Gatekeeper, AMFI and Terminal/SSH TCC policy independently of Xcode selection.
See the [security policy contract](docs/IMAGE-PIPELINE.md#guest-security-policy)
and [verified scope](docs/VERIFICATION.md) for supported and tested combinations.
