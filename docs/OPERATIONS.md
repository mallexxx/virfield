# Operations

Run one daemon for each managed set of targets. Keep installed binaries,
configuration, credentials and runtime state outside the checkout.

## Install

Local VM hosts require Apple Silicon macOS and Tart. Install Packer for image
builds. Use the Go version in `go.mod`; `make build` also builds the native Apple
sign-in browser on macOS. Set `SIGN_IDENTITY` to a stable signing identity to
retain Full Disk Access across rebuilds, particularly for external storage.

Initialize a new private deployment; `init` refuses an existing directory:

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
VIRFIELD_URL="http://127.0.0.1:7780"
./bin/virfield init "$VIRFIELD_HOME" TEMPLATE_ID GOLDEN_NAME local
```

Install `virfield`, `virfieldd` and `virfield-mcp` in its `bin` directory. Install
`VirfieldAppleBrowser.app` alongside them and register its URL scheme through
LaunchServices if Apple sign-in is needed. Cached/public artifacts do not require
that browser. Configure an approved pinned image before building or acquiring.

```sh
"$VIRFIELD_HOME/bin/virfield" config-check "$VIRFIELD_HOME/config.json"
python3 deploy/prepare-services.py "$VIRFIELD_HOME"
```

The generator writes `launchd/ai.virfield.virfieldd.plist` and `launchd/mcp.json`
without starting services and refuses an existing `launchd` directory. Review
and register the service as the VM-owning user. The [service example](../deploy/ai.virfield.virfieldd.plist.example)
is a template. Use `ProcessType=Standard` so launchd does not throttle API work
as background maintenance. Start only the registered Virfield service; Tart is
invoked as a CLI.

## Configure storage and credentials

`config.json` selects `runtime.provider: "tart"`. `state_dir` may differ from the
deployment directory. Config, token and state belong to the daemon account;
private directories are mode 0700 and credential files mode 0600. The native
browser requires private owner-controlled deployment/config/state, rejects config
and state symlinks, and pins the configured loopback origin.

A Tart runtime has an absolute binary path, a host ID and containers with stable
IDs, absolute paths and `kind: local|external`. Pin `volume_uuid` for removable
storage. Set `max_vms`, CPU/RAM/disk reserves and the default/fallback clone
locations for your hardware. A local golden cannot be cloned across `TART_HOME`
containers; use [image transfer](IMAGE-PIPELINE.md#transfer-a-local-golden) first.

This fragment uses placeholders; replace them with your paths and account:

```json
{
  "runtime": {
    "provider": "tart",
    "tart": {
      "binary": "/opt/homebrew/bin/tart",
      "host_id": "primary",
      "containers": [{"id": "external", "path": "/Volumes/VMStorage/tart", "kind": "external", "volume_uuid": "VOLUME_UUID"}],
      "registry_credentials": [{"host": "ghcr.io", "username": "REGISTRY_USER", "token_file": "/private/virfield/ghcr.token"}],
      "image": {"packer": "/opt/homebrew/bin/packer", "xcode_archives": "/private/virfield/cache/xcode"}
    }
  }
}
```

Configure approved bases in `runtime.tart.bases`; a template pins its image profile
and container. Tart reads `~/.docker/config.json`, so reconcile stale GHCR entries
with the configured credentials. Tokens stay in private files, never prompts or
MCP arguments. See [Image pipeline](IMAGE-PIPELINE.md) for profile fields.

## Physical nodes

Add nodes under `runtime.nodes`. Each needs a unique ID, address, user, OS,
architecture and trusted `host_key` in authorized-key format. Use `host_keys` for
additional verified identities of a multi-boot machine. Compare fingerprints out
of band before trusting a new key. Node IDs must differ from primary host and
storage IDs.

At first start, Virfield creates its node management key under
`state_dir/nodes/management`. Append `management.pub` once to the configured node
user's `~/.ssh/authorized_keys` on each boot volume. Nodes use unprivileged SSH;
Virfield does not acquire sudo access. Optional `wake_on_lan` specifies `mac` and
`broadcast`; `keep_awake_file` integrates with the node's own Linux sleep guard.

A physical template uses `provider: "baremetal"`, `location: NODE_ID`, its boot
volume name and `guest_user`. It is available while that environment is booted.
Offline inventory and cached probes remain visible; Wake-on-LAN is followed by a
fresh probe. Configure charger sleep settings on each macOS boot volume if it
must remain reachable. A closed laptop lid can still suspend the node.

For XCUITest, copy and run the operator setup script on that boot volume:

```sh
scp deploy/node-ui-testing.sh NODE_USER@NODE_ADDRESS:
ssh -t NODE_USER@NODE_ADDRESS 'bash ~/node-ui-testing.sh'
```

The script needs a terminal for sudo. Log that user into the console. Require the
pseudo-golden's `ui_testing` value to be `ready`; other values describe missing
developer permissions, automation authorization or a console session.

A lease adds its caller key, creates `~/virfield/leases/VM_NAME/workspace` and holds
sleep prevention. Release removes that key/workspace and releases owned holds.
It preserves pre-existing holds and does not kill shared-account workloads.

## Linux-hosted macOS lab VMs

Configure `runtime.nodes[].vms` with a digest-pinned Podman image, `goldens_dir`,
`port_base`, `max`, `cpu`, `memory_gb` and `disk_size` such as `100G`. The node user
needs rootless Podman, `/dev/kvm` and `/dev/net/tun`. Store disks on a filesystem
with enough space for full virtual growth. Check Podman's runroot filesystem too.

`storage_dir` optionally selects a private root for new `leases/` and `images/`;
it defaults to `~/virfield`. Change it only with no active jobs or overlays.
Virfield refuses a root change while remembered overlays or old VM folders remain;
it does not migrate them automatically.

Install a bootstrap qcow2 golden and matching `.opencore.plist` manually. Configure
`admin/admin`, Remote Login, passwordless sudo and the profile's SIP policy.
A node image profile declares `image.node.golden`, exact `macos`/`build` and
`disable_sip`. Build it through Virfield to qualify and commit a new read-only
secured golden. Bootstrap goldens cannot be leased directly.

Workers use qcow2 overlays. Guest SSH is published on the node's address at
`port_base` through `port_base+max-1`; image builds use the next `max` ports.
Node-loopback desktop ports are SSH-port+1000. Management traffic crosses pinned
node SSH. VM preparation has a 20-minute budget; the caller TTL includes it.
Release confirms container absence before deleting an overlay.

## Capacity

Read `resource_admission[TEMPLATE]` in status for CPU, RAM, each candidate disk and
cache arithmetic. Unknown metrics block admission. The controller counts external
running VMs, durable reservations, full potential disk growth and safety reserves,
then rechecks before dispatch. An explicit destination never silently falls back.
A changed budget waits under the original job/key/deadline.

Nodes have separate physical-host and VM slots. VM budgets default to observed
CPU, RAM minus 4 GiB and a 20 GiB disk reserve. Override bounded positive values
through `vms.resource_limits`. Optional `cache_limit_bytes` caps allocated golden
storage plus pending full-size builds; zero leaves it uncapped. Virfield never
prunes images automatically. Physical-account workloads are not CPU/RAM sandboxed.

## CLI, SSH and desktop

Use `virfield -help` for syntax and the installed URL/token file for API calls.
Save one stable request key per mutation. Generate a fresh caller identity, acquire
and wait for both successful job and ready lease:

```sh
virfield keygen /absolute/new/private/identity
virfield -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token" -key task-acquire-001 acquire TEMPLATE 3600 /absolute/new/private/identity/id_ed25519.pub
virfield -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token" job JOB_ID
virfield -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token" ssh-config LEASE_ID /absolute/new/private/identity
ssh -F /absolute/new/private/identity/config virfield
```

Generated configuration pins the host key and disables agent/password fallback.
VM workspaces are `~/workspace`. Export results before authorized release.
TTL is 60–86400 seconds; renewal takes a future RFC3339 timestamp within 24 hours
and never shortens the deadline.

`virfield desktop LEASE_ID [--print]` and MCP `vm_tunnel` with `service: desktop`
open Screen Sharing for ready VMs through daemon-host loopback. Keep the URL
private: it contains the console password. Physical hosts have no desktop tunnel.
Release, expiry, confirmed drift and explicit close revoke tunnels. A remote
client's localhost differs from the daemon's localhost. See [remote debugging](REMOTE-DEBUG.md)
for generated LLDB workspaces.

## MCP and remote access

Help topics `workflows`, `images`, `operations`, `verification` are embedded and
also available at `virfield://docs/TOPIC`.

Tools: `virfield_help`, `virfield_status`, `vm_acquire`, `vm_lease`, `vm_revalidate`, `vm_release`, `vm_renew`,
`virfield_job`, `virfield_events`, `image_catalog`, `image_create`, `image_apple_auth`,
`image_build`, `image_transfer`, `image_pull`, `image_publish`, `registry_sources`, `registry_resolve`,
`vm_tunnel`, `vm_tunnel_close`.

The API binds to loopback and enforces a Host allowlist. Remote access requires an
authenticated TLS proxy. Scoped REST principals support `lease:own` and
`image:build`; HTTP MCP uses the operator principal.

### Register Codex and Claude Code

Preserve unrelated server entries when registering the installed adapter:

```sh
codex mcp get virfield
claude mcp get virfield
codex mcp add virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
claude mcp add --transport stdio --scope user virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
```

Claude Desktop has separate configuration. Reconnect each owning client after an
adapter upgrade, then read help/status in that client. A new test process cannot
verify which adapter an existing chat still uses.

## Upgrade and restore

Build outside live state. Preserve configuration, credentials, journal and installed
binaries; record binary hashes. Wait for active leases/jobs to drain, replace the
configured binaries and gracefully restart the exact registered service. Verify
health, inventory, hashes and preserved image identities. Reconnect MCP clients.

Shutdown closes admission, allows an image stage ten minutes to drain, then cancels
its Packer/SSH context and allows five more minutes for interruption, stop and
persistence. Unknown dispatched mutations remain reserved for inspection.

The CLI `backup` command snapshots SQLite, configuration, owner-token hash, image
credentials, node management keys and cached probes into an owner-only directory;
five complete snapshots are retained. It excludes VM disks. Back up separately
any external credential files referenced by config. Use `virfield -help` for backup syntax. To restore, stop the
service and retain the current state, then restore the snapshot’s database, image
identities and node directory to the configured private state path. Restore config
and external credentials separately, preserving ownership and modes. Create a new
operator token and start exactly one manager.
A binary rollback needs a state snapshot compatible with that binary's schema;
pre-migration snapshots are private and integrity checked.

For full disk recovery, stop the exact owned VM, copy disk/config/NVRAM to
independent storage and compare hashes before disturbing the source. Retain the
original outside `TART_HOME/vms` until restored identity, pinned SSH, native
readiness and workspace data are verified. Tart enumerates dot directories too.
Delete recovery fixtures only after their data is safe and cleanup is authorized.

## Recovery and diagnostics

A lost response must reuse its original body and request key. Inspect the exact
target and confirm no operation is in flight before recovery. Never edit SQLite,
mark an image ready manually or bypass the journal through runtime directories.

`resolve` queues cleanup and can delete the VM. Export needed data and obtain
cleanup authorization before running:

```sh
virfield -token-file "$VIRFIELD_HOME/token" -key inspected-cleanup-001 resolve LEASE_ID EXACT_VM_NAME CONFIRM-NO-OPERATION-IN-FLIGHT
```

For read-only recovery of an owned unexpired VM with `vm_drift`, use its original
identity and complete verified baseline:

```sh
virfield -token-file "$VIRFIELD_HOME/token" -key inspected-revalidation-001 revalidate LEASE_ID EXACT_VM_NAME EXISTING_IDENTITY_DIRECTORY
virfield -token-file "$VIRFIELD_HOME/token" job JOB_ID
virfield -token-file "$VIRFIELD_HOME/token" lease LEASE_ID
```

`revalidate-proof` prints the public signed request for REST/MCP. Its signature
binds lease, VM and request key; the private key stays local. Revalidation checks
existing runtime/image identity, host pin, management/caller keys, policy and
native readiness. It preserves workspace, keys and expiry. Physical hosts and
records without a verified baseline are unsupported. Wait for job success and
lease readiness; existing connection files are not overwritten.

Events retain typed drift/backend/listener/stream diagnostics, up to 32 records
per kind per lease under normal history retention. Save `next_cursor` and allow
ID gaps. Raw payloads, credentials and desktop URLs are excluded. Use fingerprints
to correlate private logs; do not infer an unknown cause from a later success.
See [Image recovery](IMAGE-PIPELINE.md#recovery) for image jobs.

## VM keychain readiness

VMs use `admin/admin` without random account-password rotation. A new clone can
convert a known inherited password while preserving keychain items; an unknown
password fails readiness. Build a new golden rather than securing an older
credential policy in place. Physical-host keychains are unchanged.

Preparation preserves the login default, creates an additional private keychain
and an Aqua agent, and installs a VM-only SSH command bridge. Native add/read/data
comparison/delete must work in both GUI and separate SSH audit sessions. The
console must be logged in, unlocked and unobstructed; worker readiness also reads
an authenticated Screen Sharing RFB greeting. Ordinary SSH/SCP commands need no
manual keychain unlock.

For `guest_keychain_failed`, preserve host `state_dir/leases/LEASE_ID/keychain.log`
or `images/IMAGE_ID/keychain.log` before release. Inspect the private guest
`~/.virfield-keychain/agent.log` if accessible. `desktop.log` and
`desktop-command.log` distinguish console waiting, SSH channel/exec delay and
command timeout. Public errors omit those logs and keychain contents. Diagnose
the failed substage; do not reset keychains, delete login items or weaken readiness.
