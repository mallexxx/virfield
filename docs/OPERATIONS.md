# Operations

This is the single runbook for installing and operating the Go implementation.
Commands below run from the repository root unless an installed binary path is
shown. VM operations require an Apple Silicon macOS host and a separate Lume
service. Tests and compilation also run on Linux.

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

## New installation

1. Run `make check` and `make build`. Initialize a new directory and install the
   binaries independently of the checkout:

   ```sh
   ./bin/virfield init "$VIRFIELD_HOME" macos27 macos-27-golden home
   mkdir -m 700 "$VIRFIELD_HOME/bin"
   install -m 755 bin/virfield bin/virfieldd bin/virfield-mcp bin/virfield-lume "$VIRFIELD_HOME/bin/"
   ```

2. Prepare the pinned Lume and image dependencies using
   [Image pipeline](IMAGE-PIPELINE.md#profile-and-dependencies). Edit the generated
   `config.json` with that document's image profile and absolute `image_tools`
   paths. `init` creates a skeleton, not a verified image. For the full UI-test
   profile, configure `provision: uitest-27-v1` and a compatible Xcode source.

3. Configure both [manager](../deploy/ai.virfield.virfieldd.plist.example) and
   [Lume](../deploy/ai.virfield.lume.plist.example) service examples as described
   below. Register Lume first, then the manager. Never start another daemon
   beside an existing installation; a per-user kernel lock also prevents this.

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

Use system LaunchDaemons running as the existing VM-owning user, never root.
A GUI LaunchAgent can be denied guest SSH by macOS Local Network Privacy even
when the same terminal command works. See Apple's
[TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy).
Host SIP, Gatekeeper and TCC do not need to be weakened.

Copy the two example plists to `"$VIRFIELD_HOME/launchd/"`, remove `.example`
from their names and replace every `VM_OWNER` and `/ABSOLUTE/DEPLOYMENT`
placeholder. Use absolute paths: launchd does not expand shell variables or `~`.
The Lume service’s `-binary` path must match `image_tools.lume` in the manager
configuration. Check each with `plutil -lint` before registration.

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
install a clean committed build. Restart the exact manager job; restart Lume
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

Use [deploy/mcp.json.example](../deploy/mcp.json.example), replacing the deployment
placeholder with an absolute path. Configure the client entry as `virfield` and
restart/reconnect the client after changing its command. No npm/tsx entrypoint
is supported. The stdio adapter calls the same authenticated daemon API.

Tools: `virfield_status`, `vm_acquire`, `vm_lease`, `vm_release`, `vm_renew`,
`virfield_job`, `virfield_events`, `image_build`, `vm_tunnel`, `vm_tunnel_close`.
HTTP MCP is served at `/mcp` and requires the same owner bearer token.

The daemon binds only to loopback. Broker/container access needs an explicit
TLS reverse proxy, network policy and tested routing to guest SSH or a tunnel.
The client rejects remote cleartext HTTP and credential-bearing redirects.
Per-principal credentials and the Balda/Broker/Runner integration are not yet
implemented; see [verification limits](VERIFICATION.md#not-yet-accepted).

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

`resource_limits` configures CPU, RAM and host disk reserve; `storage_paths` maps
Lume location names to absolute directories. Defaults leave 25% of CPU/RAM and
20 GiB disk free. Admission includes external VMs and reservations. Unknown
resources block admission. Sparse/APFS clone disks still reserve full potential
growth, not just their current allocated blocks.

`backup` requires an idle healthy pool and no unresolved jobs. It snapshots
SQLite, the actual configured config/token files and active image identities to
an owner-only directory in `backups/`; five completed snapshots are retained.
**VM disks and the IPSW cache are not included.** Back them up separately.

To restore, stop the manager, preserve current state and copy a complete snapshot
into a new private directory. Adjust `state_dir` and `token_file`, verify the
corresponding stopped Lume disks still exist and start exactly one manager.
Never restore older credentials against a VM whose SSH identity has since changed.
