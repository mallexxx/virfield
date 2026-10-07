# Operations

This is the single runbook for installing and operating the Go implementation.
Commands below run from the repository root unless an installed binary path is
shown. VM operations require an Apple Silicon macOS host and a separate Lume
service. Tests and compilation also run on Linux. VM hosting requires Apple Silicon; Intel
Macs, Linux and Windows are not supported VM hosts. Catalog image creation selects macOS and Xcode versions. The fully accepted UI-test recipe
is macOS 27 build 26A428; see the tested scope in [Verification](VERIFICATION.md).

## Paths and configuration

Choose one private, absolute deployment directory outside the checkout and set
`VIRFIELD_HOME` to it. A new installation can use:

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
```

The accepted existing host uses `~/.virfield-v2`; that is an active deployment
path, not a source directory. Do not rename a running deployment or initialize
an existing state directory. All clients and service plists must reference the
same deployed binaries, config and token. Lume owns its separate VM storage
(default `~/.lume`). Moving or checking out source code does not move VM disks.

| Deployment path | Contents |
|---|---|
| `bin/` | Four installed Go binaries |
| `config.json`, `token`, `state.db` | Host configuration, bearer token, durable journal |
| `images/`, `leases/` | Private guest identities and bounded diagnostics |
| `cache/`, `tools/` | Verified restore media and host image-tool dependencies |
| `backups/`, `launchd/` | State snapshots and configured service plists |

Directories containing state or secrets must be mode 0700; token, database and
credential files must be 0600. Do not store these files in Git.

## GHCR configuration

Add operator-approved sources to `config.json` before starting the service:

```json
{
  "registries": [
    {"id":"public-cua","organization":"trycua"},
    {"id":"team","organization":"YOUR_LOWERCASE_GITHUB_ORG","username":"YOUR_GITHUB_USER","token_file":"/absolute/private/ghcr-token","allow_push":true}
  ]
}
```

This is a fragment, not a replacement configuration. Use actual lowercase
GitHub namespace/user values. `token_file` is a regular owner-only file (0600)
inside a private directory (0700), containing a GitHub package token, not JSON.
Public pull needs no token. Private pull requires package read permission;
publish needs package write permission and `allow_push: true`. Configure any
organization SSO authorization outside Virfield. Token contents never belong in
Git, API/MCP arguments or command-line flags. The subprocess receives only the
selected source credential, not all host environment credentials. Each registry
subprocess uses isolated Lume settings with the explicit configured storage path;
personal Lume registry defaults cannot redirect an upload. Spaces in paths are
supported; quote, backslash and line-break characters are rejected by this
fixed YAML configuration boundary.

Configure explicit `storage_paths` for every import location. Existing VM names
and existing registry tags are refused. Import/export admission requires an idle
pool. An accepted image job reserves its own VM slot; another verified image
can lease the remaining slot. `/status.image_reservations` names each reserved
image, job, start time and deadline. The deadline is a timeout, not an ETA.
Sources are trusted software publishers: importing their disk can run their
code inside a guest. Only Lume VM formats are supported, not arbitrary OCI/Tart
packages. Current disk limit is 512 GiB; allow space for compressed cache and
expanded disk. The pinned Lume decoder is not an untrusted-input sandbox.

Use MCP's workflows topic for arguments and completion criteria. CLI equivalents:

```sh
virfield registry-sources
virfield registry-resolve team macos-golden build-001
virfield -key import-build-001 image-pull imported-golden team macos-golden build-001 15.7
virfield -key publish-build-002 image-publish imported-golden team macos-golden build-002
```

Use your installed binary and normal `-url`/`-token-file` flags. Publication
rebuilds the verified recipe into a portable image; it does not export private
source-disk contents or manual modifications. The uploaded image has public
bootstrap credentials. For package retention or remote deletion use GitHub's
operator tooling; Virfield never silently deletes remote packages/tags. New tag
checks do not provide an atomic lock against unrelated GHCR writers.
For catalog create/pull into a configured nondefault storage location, pass
`-location LOCATION` before the command. `events` accepts `-lease-id ID`,
`-tail` and `-limit N` (1–500) before the command.

## Prerequisites

For a source build, install Git, make, Go at least the patch pinned in `go.mod`,
and Python 3.10 or newer for repository checks. Go may download its pinned
toolchain. Access to GitHub, the Go module proxy/checksum database, Apple restore
media and the image-tool package repositories is required during setup.

For VM hosting, use an Apple Silicon Mac, a non-root administrator account that
owns the VMs, and full Xcode with its license/first-launch setup completed. The
image dependency recipe uses Swift from Xcode, Python 3.14 with its pinned VNC
environment, and Tesseract with English OCR data. The optional UI-test profile
also needs a local Xcode 27 or newer bundle. Allow at least 100 GiB free for the
initial image/download plus space for Xcode and clones; admission enforces the
actual configured disk/CPU/RAM reserve. Two simultaneous 4-CPU/8-GiB guests need
at least 12 logical host CPUs and 32 GiB RAM under the default 25% reserve.

The published v2.0.0 release archive includes four prebuilt macOS/arm64 binaries, source, docs,
deployment tools, dependency license notices and a revision manifest. Verify its
`SHA256SUMS` before extraction. These are Go/ad-hoc-signed binaries, not an Apple
notarized installer. Lume, Python packages, Tesseract, Xcode and IPSW media are
installed separately; none are taken from the release author's machine.

## New installation

1. Run `make check` and `make build` from a source checkout, or use `bin/` from
   the verified release archive. Initialize a new directory and install the
   binaries independently of the checkout:

   ```sh
   mkdir -p "$(dirname "$VIRFIELD_HOME")"
   ./bin/virfield init "$VIRFIELD_HOME" macos27 macos-27-golden home
   mkdir -m 700 "$VIRFIELD_HOME/bin"
   install -m 755 bin/virfield bin/virfieldd bin/virfield-mcp bin/virfield-lume "$VIRFIELD_HOME/bin/"
   cp -R bin/VirfieldAppleBrowser.app "$VIRFIELD_HOME/bin/"
   /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$VIRFIELD_HOME/bin/VirfieldAppleBrowser.app"
   ```

2. Prepare the pinned Lume and image dependencies using
   [Image pipeline](IMAGE-PIPELINE.md#profile-and-dependencies). Edit the generated
   `config.json` with that document's image profile and absolute `image_tools`
   paths. `init` creates a skeleton, not a verified image. For the full UI-test
   profile, configure `provision: uitest-27-v1` and a compatible Xcode source.

   `make build` also builds `VirfieldAppleBrowser.app`, a private WebKit window
   that opens Apple's Developer website. Its URL scheme must be registered with
   LaunchServices after installation. The browser passes only the scoped Apple
   download session to `state_dir/apple-auth/cookies.txt` (mode 0600); Apple
   Account credentials and 2FA are entered on Apple's site. The API remains
   loopback-only: open the one-time link on the Virfield Mac and send it privately.
   The API also returns `deep_link` for a direct native-app launch. Its `url`
   field is a one-click HTTP fallback that redirects to the app without a launch
   page; append `?manual=1` only if the browser blocks the redirect. The app
   closes after a successful session or when its window is closed. Install the
   app under `state_dir/bin`: it reads the adjacent `config.json` and refuses
   deeplinks that point to a different loopback port.

3. Generate the service and MCP files from the completed configuration, running
   as the VM owner without sudo:

   ```sh
   python3 deploy/prepare-services.py "$VIRFIELD_HOME"
   ```

   Review the generated files and register Lume first, then the manager as below.
   Never start another daemon beside an existing installation; a per-user kernel
   lock also prevents this.

4. Use the installed CLI to build the configured golden, inspect its job, and
   wait for `image_ready` before acquiring leases:

   ```sh
   "$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" -key golden-build-001 image-build macos27
   "$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" job JOB_ID
   "$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" status
   ```

The console is at `http://127.0.0.1:7780`. Enter the deployment token locally;
the browser retains it only in memory and clears the input after connection.
Status reads the journal and last reconciler observation, not a new Lume request
per browser poll. A cold or failed observation blocks admission.

## System services

Give Broker a separate token through the optional `principals` configuration:

```json
{"principals":[{"name":"execution-broker","token_file":"/absolute/private/broker-token","scopes":["lease:own","image:build"]}]}
```

Point Broker's Virfield worker `token_file` at that same private file. Its
`lease:own` scope permits only leases it created, including `GET /api/v1/leases`
for orphan discovery; an operator can filter that endpoint with `?owner=NAME`.
`image:build` permits catalog,
create/build, image-job reads and Apple-link issuance. Recovery, deletion,
publication, backup, status and direct MCP remain operator-only. Existing
unowned leases remain operator-only. Replace token files atomically and send
`SIGHUP` to `virfieldd` to reload owner and principal tokens; a failed reload
leaves the previous credentials active. The Broker plugin's image tools use this
REST principal through its authenticated MCP connection.

Use system LaunchDaemons running as the existing VM-owning user, never root.
A GUI LaunchAgent can be denied guest SSH by macOS Local Network Privacy even
when the same terminal command works. See Apple's
[TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy).
Host SIP, Gatekeeper and TCC do not need to be weakened.

`deploy/prepare-services.py` renders the [manager](../deploy/ai.virfield.virfieldd.plist.example)
and [Lume](../deploy/ai.virfield.lume.plist.example) templates into the private
`launchd/` directory. It uses the invoking user's account, group and actual HOME,
the configured tool paths and ports, and refuses existing output or insecure
config/token permissions. Paths with spaces are preserved as individual arguments.
It starts no services and never copies a token value into MCP configuration.

Use absolute paths: launchd does not expand shell variables or `~`. The managed
Lume endpoint is `http://127.0.0.1:PORT`; its wrapper takes the stable
`-config` path, reads `image_tools.lume` when launchd starts it, requires an
explicit `-log` path and accepts `-port`. This keeps the root LaunchDaemon stable
across Lume binary upgrades; update the private config or stable tool path and
restart the service. Check both generated plists with `plutil -lint` before
registration.

For a **new installation only**, administrator authorization is required:

```sh
sudo install -o root -g wheel -m 644 "$VIRFIELD_HOME/launchd/ai.virfield.lume.plist" /Library/LaunchDaemons/ai.virfield.lume.plist
sudo install -o root -g wheel -m 644 "$VIRFIELD_HOME/launchd/ai.virfield.virfieldd.plist" /Library/LaunchDaemons/ai.virfield.virfieldd.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/ai.virfield.lume.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/ai.virfield.virfieldd.plist
```

Lume is an independent job wrapped by `virfield-lume`; the manager never restarts
it automatically. Service logs rotate at 10 MiB with three archives. Startup
stderr is a separate diagnostic file. Inspect `launchctl print system/LABEL`
and authenticated `status` after registration; a successful command alone does
not establish fresh backend inventory.

For an update, first obtain an idle healthy pool with no unresolved jobs and all
VMs stopped. Back up the state, preserve the installed binaries/config, then
install a clean committed build. Schema 7 adds retention indexes; schema 6
records registry imports and portable-export jobs (schema 5 added catalog
templates). The manager creates and checks a private SQLite snapshot in
`migration-backups/` before changing an existing database. The migration fails
closed if that snapshot cannot be written or verified. Ensure enough free disk
for a full extra database copy. Older binaries refuse schema 7. Rollback
requires the matching pre-upgrade backup,
not merely replacing the executable. Restart the exact manager job; restart Lume
only if its binary or service configuration changed. Never unload or kill Lume
while a VM is active. Verify both services, fresh inventory and MCP before
accepting the release. An ambiguous `bootstrap` failure requires inspection of
the existing job, not repeated unload/reload commands or a second daemon.

## Leases and SSH

Generate a new caller-owned key per lease. Private keys are never uploaded:

```sh
"$VIRFIELD_HOME/bin/virfield" keygen /absolute/private/story-42
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" -key story-42-attempt-1 acquire macos27 3600 /absolute/private/story-42/id_ed25519.pub
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" lease LEASE_ID
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" job JOB_ID
```

Wait for `ready`, then export the pinned SSH configuration:

```sh
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" ssh-config LEASE_ID /absolute/private/story-42
ssh -F /absolute/private/story-42/config virfield
```

Export verifies the matching local private key and refuses to overwrite files.
It pins the guest host key and disables password/agent authentication and SSH
connection sharing. `tunnel LEASE_ID` opens a loopback forwarding endpoint;
`tunnel-close LEASE_ID` closes it. Reopen a tunnel after daemon restart. Direct
guest-IP access is also supported. Tunnels close on expiry, release or drift.

For a SwiftPM worktree, generate VS Code build, test and debug actions after the
SSH config exists:

```sh
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" \
  vscode-config LEASE_A /absolute/private/story-42 /absolute/path/to/worktree \
  LEASE_B /absolute/private/story-43
```

This builds and tests on the host, incrementally syncs runtime artifacts, and
connects host LLDB-DAP to guest `debugserver` through a loopback SSH tunnel. A
read-only NFS export supplies the dyld cache without gdb-remote image copies. See
[VS Code remote debugging](REMOTE-DEBUG.md). The generated `.code-workspace`
offers every supplied VM in the Run/Debug dropdown and includes tasks for the
Virfield web console and each VM's pinned SSH terminal.

Golden verification prepares the corresponding Xcode `macOS DeviceSupport`
directory on each host. Registry-pulled developer images run the same verify
stage, so their system symbols are prepared before the image becomes ready.

`renew LEASE_ID FUTURE_RFC3339` extends expiry within 24 hours; an earlier value
never shortens it. Repeating the same acquisition request and idempotency key
returns the original lease, including after release. Reusing that key with a
different request returns `idempotency_conflict`. An active lease's public key
cannot be reused for a different lease.

Release and expiry permanently delete the disposable VM. Export artifacts first:

```sh
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" -key story-42-release-1 release LEASE_ID
```

The CLI's `-help` is the command/argument reference. Capacity is at most two VMs;
external running VMs and reserved leases count. A full pool returns an explicit
`capacity_exhausted` message. Execution queueing belongs to Broker.

## MCP and remote access

Use the generated `"$VIRFIELD_HOME/launchd/mcp.json"`; it includes the configured
daemon URL and token-file path. The [generic example](../deploy/mcp.json.example)
is also available for clients using the default port. Configure the client entry as `virfield` and
restart/reconnect the client after changing its command. No npm/tsx entrypoint
is supported. The stdio adapter calls the same authenticated daemon API.

Start with `virfield_help` (the embedded [agent guide](AGENT-GUIDE.md)).
Its topics `workflows`, `images`, `operations`, and `verification` also exist as
MCP resources at `virfield://docs/TOPIC`. No checkout or Obsidian is required.

Tools: `virfield_help`, `virfield_status`, `vm_acquire`, `vm_lease`, `vm_release`, `vm_renew`,
`virfield_job`, `virfield_events`, `image_catalog`, `image_create`, `image_apple_auth`,
`image_build`, `image_pull`, `image_publish`, `registry_sources`, `registry_resolve`,
`vm_tunnel`, `vm_tunnel_close`.
Use `image_create` to select versions instead of restricting an agent to configured
templates; see [version selection and Apple authentication](IMAGE-PIPELINE.md#choose-macos-and-xcode-versions).
HTTP MCP is served at `/mcp` and requires the same owner bearer token.

The daemon binds only to loopback. Broker/container access needs an explicit
TLS reverse proxy, network policy and tested routing to guest SSH or a tunnel.
The client rejects remote cleartext HTTP and credential-bearing redirects.
Scoped principals and Broker image tools are implemented in the source, but
container ingress and the full Balda workflow still need live acceptance; see
[verification limits](VERIFICATION.md#not-yet-accepted).

### Register Codex and Claude Code

Register only an accepted installed release, after the production checks in
[Verification](VERIFICATION.md) pass. Registration does not upgrade the daemon or
prove image readiness. Use the existing deployment's actual absolute
`VIRFIELD_HOME` and configured `VIRFIELD_URL`; do not initialize a second pool.
Inspect only the `virfield` entry before changing it; preserve unrelated servers.

```sh
codex mcp get virfield
claude mcp get virfield
```

A missing entry is expected for first installation. After acceptance, register
stdio adapters with the token **file path**, not its contents:

```sh
VIRFIELD_URL="http://127.0.0.1:7780"
codex mcp add virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
claude mcp add --transport stdio --scope user virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
```

Codex's desktop/CLI clients share MCP configuration on the same host. Claude
Code's user scope makes the entry available across projects. If an existing entry
conflicts, update only that entry using the client's supported configuration;
do not overwrite the whole config or add a second server/pool. Client policies
may require enabling/approving the server. Restart/reconnect the client and start
a fresh task if its tool list is stale. Use `codex mcp get virfield`,
`claude mcp get virfield`, and the client's `/mcp` status to check discovery.

In each client, call `virfield_help` and `virfield_status`. Complete the agent
workflow acceptance in [Agent guide](AGENT-GUIDE.md#client-registration-and-acceptance).
Never use a successful registration command as evidence that tools were invoked
in that client. Keep a release pending until both client checks pass.

Claude Desktop is a separate client from Claude Code. For Desktop use the generated
`launchd/mcp.json` entry through its MCP configuration; the `claude mcp add` command
above configures Claude Code. No remote/container reachability is implied by either.

Client references: [Codex MCP configuration](https://developers.openai.com/codex/mcp/),
[Claude Code MCP configuration](https://code.claude.com/docs/en/mcp).

## Recovery

Read the job's error and inspect the exact VM. Do not edit SQLite or delete state
to free capacity. A dispatched mutation with an unknown outcome is not replayed.
Once inspection confirms no operation remains in flight, authorize exact-VM
cleanup with:

```sh
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" -key inspected-resolution-001 resolve LEASE_ID EXACT_VM_NAME CONFIRM-NO-OPERATION-IN-FLIGHT
```

This operation is absent from MCP. Unowned collisions and protected golden
images cannot be deleted through lease recovery. If Lume is unavailable, retain
the reservation and restore that service separately. Image recovery has its own
[stage-specific contract](IMAGE-PIPELINE.md); do not use lease recovery for images.

## Quotas and backups

Portable GHCR images contain a known `lume/lume` guest account with password SSH
and passwordless sudo. Automation images may also have SIP, Gatekeeper and AMFI
disabled. Treat each image as a privileged machine snapshot: keep its registry
package private and restrict pull access. The isolated per-lease SSH identity is
installed only when Virfield clones the image; it does not change the published
base image's credentials.

`resource_limits` configures CPU, RAM and host disk reserve; `storage_paths` maps
Lume location names to absolute directories. Defaults leave 25% of CPU/RAM and
20 GiB disk free. Admission includes external VMs and reservations. Unknown
resources block admission. Sparse/APFS clone disks still reserve full potential
growth, not just their current allocated blocks.

`default_clone_location` and `fallback_clone_locations` select clone destinations
from `storage_paths`. When an acquire request omits `destination_location`, the
manager tries the default and then fallbacks in order, skipping locations that
are unavailable or lack disk space. Without a configured default, the template's
location remains the default. An explicit `destination_location` is strict and
fails instead of falling back. CPU and RAM failures never trigger storage fallback.

`virfield-lume` synchronizes available `storage_paths` into Lume whenever the
service starts. It leaves Lume's own default at `home`, does not remove unmanaged
locations, and skips an unavailable path so clone fallback can still operate.
Cross-storage clones use a longer bounded operation window because they copy the
disk instead of using an APFS clone on the source volume.

```json
{
  "storage_paths": {
    "home": "/Users/operator/.lume",
    "external": "/Volumes/VMs/lume"
  },
  "default_clone_location": "external",
  "fallback_clone_locations": ["home"]
}
```

`backup` requires an idle healthy pool and no unresolved jobs. It snapshots
SQLite, the configured settings, a SHA-256 fingerprint of the owner token, and
active image identities to an owner-only directory in `backups/`; five completed
snapshots are retained. The raw owner token is never copied into a backup.
**VM disks and the IPSW cache are not included.** Back them up separately.

The manager prunes journal history at startup and every 24 hours. It removes
released leases last updated more than 30 days ago, their completed jobs and
idempotency requests, and events older than 30 days. An unfinished job keeps its
lease, jobs and request keys. Active leases, their jobs and request keys remain
until release. Reusing an expired idempotency key after pruning starts a new
request. Migration snapshots are kept in `migration-backups/` for operator
rollback; this policy does not delete them or media caches.

To restore, stop the manager, preserve current state and copy a complete snapshot
into a new private directory. Generate a new owner token, adjust `state_dir` and
`token_file`, reconfigure clients with that new token, and verify the
corresponding stopped Lume disks still exist and start exactly one manager.
Never restore older credentials against a VM whose SSH identity has since changed.
