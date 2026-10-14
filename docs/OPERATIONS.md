# Operations

This runbook describes current Tart + SSH nodes. Virfield does not use Lume; the
separate old LaunchDaemon was disabled and booted out on 2026-10-14, its rollback files preserved.
[Historical Lume operations](history/OPERATIONS-before-2026-10-14.md)
are archived. Current defects and acceptance limits are in
[Verification](VERIFICATION.md). Orchard is not deployment-ready.

## Paths

A deployment directory owns installed binaries, `config.json`, the operator
token and generated service/client configuration. `state_dir` may be a separate
private directory. Runtime disks stay in configured Tart containers or node
storage. The checkout is never runtime state.

The existing personal installation uses `~/.virfield-v2` for deployment and
`~/.virfield-tart` for state; the old directory name is not a runtime/version
claim. Do not initialize another installation or change its port to upgrade it.
Generic examples use:

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
VIRFIELD_URL="http://127.0.0.1:7780"
```

## Configuration and prerequisites

Local VM hosts require Apple Silicon macOS, Tart and private storage. Image
construction additionally requires Packer and a verified Xcode XIP cache.
`runtime.provider` selects Tart; `runtime.nodes` adds pinned SSH nodes.
Containers have stable IDs, absolute paths, local/external kind and, for removable
storage, a pinned volume UUID. CPU/RAM/disk reserve and VM slots are explicit.
Physical-node and node-VM resource safety limitations are recorded in Verification.

Config/token/state must belong to the daemon account. Tokens and credential files
are owner-only; state is mode 0700. Never copy token values into client config.

### Physical nodes

`runtime.nodes` adds physical hosts beside the Tart runtime. Virfield controls
them over pinned SSH as one unprivileged account (no sudo), and a node lease
uses that node's own slot, never a Mac VM slot or Mac CPU, memory or disk.

```json
"nodes": [
  {"id": "mbp-2014", "address": "192.168.1.79", "user": "admin", "host_key": "ssh-ed25519 AAAA…",
   "host_keys": ["ssh-ed25519 AAAA… (Big Sur)"], "os": "macOS", "arch": "amd64"},
  {"id": "htpc", "address": "192.168.1.86", "user": "bazzite", "host_key": "ssh-ed25519 AAAA…", "os": "linux", "arch": "amd64",
   "keep_awake_file": "/tmp/rsync-active", "wake_on_lan": {"mac": "B0:6E:BF:60:6D:64", "broadcast": "192.168.1.255:9"}}
]
```

Take `host_key` from a trusted `known_hosts` entry and compare its fingerprint
with the node. On first start `virfieldd` creates its node management key in
`state_dir/nodes/management`; append `state_dir/nodes/management.pub` once to
the node account's `~/.ssh/authorized_keys` (each macOS boot volume has its
own home and is enrolled when booted).

Each boot volume of a multi-boot Mac also has its own SSH host key, so the
same address presents a different identity per booted system. List the extra
identities in `host_keys`: Virfield accepts any configured key and reports the
one presented in the lease's `ssh.host_key`. For an interactive client, keep
one `known_hosts` line per identity for the address (OpenSSH accepts any
matching line); `ssh-keyscan -t ed25519 <address>` while the new volume is
booted gives the key, which you compare out of band before trusting it.

Node IDs must differ from the primary Tart host ID and all storage-container IDs.
Ambiguous runtime host ownership blocks admission instead of changing routing.

A node reports its booted environment as a pseudo-golden named after the boot
volume (`Monterey`, `Big-Sur`, `bazzite`). A template with
`"provider": "baremetal"`, `"location": "<node id>"`, `"name": "<volume>"` and
`guest_user` is available only while that volume is booted. An unreachable
node is `offline` (or `asleep` with Wake-on-LAN, then still admissible and
woken at start); it is re-probed at most every 30 seconds, and an online node
at most once a minute so inventory never keeps it awake. Each node's last probe is
kept in `state_dir/nodes/<id>.probe.json`, so a daemon restart while a node
sleeps keeps it admissible and keeps its leases in inventory; the first
observation after a restart still dials every node.

A MacBook node is kept awake on its charger by the node itself, once per
boot volume (each volume has its own power settings):

```bash
ssh -t admin@192.168.1.79 'sudo pmset -c sleep 0 disksleep 0 standby 0 autopoweroff 0 powernap 0 displaysleep 10 womp 1 tcpkeepalive 1'
```

Applied on Ventura (2026-10-08); Monterey, Big Sur and Catalina still need
it. Closing the lid still sleeps the Mac unless `pmset -a disablesleep 1`.
On Catalina, if the Sharing pane hangs on "Remote Login starting…", start SSH
with `sudo launchctl load -w /System/Library/LaunchDaemons/ssh.plist`.

UI tests (XCUITest) on a macOS node need Developer Mode on that boot volume and
Automation Mode that does not require authentication
(`automationmodetool enable-automationmode-without-authentication`, present
from macOS 12.6); otherwise testmanagerd asks for an admin password on the
node's display and the run fails with "Timed out while enabling automation
mode". This lets any process of that account drive the UI without a password,
which is intended for a dedicated test node. Virfield has no sudo on nodes, so
set both once per boot volume:

```bash
scp deploy/node-ui-testing.sh admin@192.168.1.79:
ssh -t admin@192.168.1.79 'bash ~/node-ui-testing.sh'
```

Copy the script first: piped over stdin, sudo has no terminal to prompt on.
Each boot volume presents its own host key (see above); a lease's generated
`ssh-config` already pins the booted volume's key, so `ssh -t -F <config> virfield`
works without editing `known_hosts`.

The node account must also own the console session (log it in on the display,
or enable automatic login). The probe reports the result on the node's
pseudo-golden as `ui_testing`: `ready`, `devtools_disabled`,
`automation_mode_requires_auth` or `no_console_session`.

A lease authorizes the client key in `authorized_keys` (tagged
`virfield-lease-<vm>`), creates `~/virfield/leases/<vm>/workspace`, and keeps
the node awake (`caffeinate` on macOS, the `keep_awake_file` on Linux, removed
only after the last reference if Virfield created it; pre-existing holds are preserved). Release revokes the key and removes the
workspace. Physical hosts are not disposable: processes the lease started
under the shared account are not killed.

### macOS VMs on a Linux node (R2)

A Linux node with `vms` runs disposable macOS VMs as rootless Podman
containers of a digest-pinned `dockurr/macos` image (QEMU + OpenCore). No sudo
or libvirt group is needed; the node account must be able to use `/dev/kvm`
and `/dev/net/tun`.

```json
"vms": {"image": "docker.io/dockurr/macos@sha256:08d1bcaac74ad44d548b7ce683b3dded083512d2a0d26416f9a620b763f8ea3d",
        "goldens_dir": "/run/media/system/Data/virfield/goldens",
        "port_base": 22100, "max": 2, "cpu": 4, "memory_gb": 8, "disk_size": "100G"}
```

- **Bootstrap goldens** are installed once by hand (see the
  [R2 spec](superpowers/specs/2026-10-08-hackintosh-nodes-r2-design.md)):
  `admin`/`admin`, Remote Login, passwordless sudo, SIP disabled through
  `<golden>.opencore.plist` (`csr-active-config` `0x7F`). They are never
  leased: a clone without a verified image is refused.
- **Secured goldens** are built by Virfield from a template whose image is
  `{"node": {"golden": "<bootstrap name>"}, "macos": …, "build": …,
  "disable_sip": true}` (`POST /api/v1/images/<template>/build`). The job
  clones the bootstrap golden into `~/virfield/images/<template>`, publishes
  its SSH on the node loopback only, rotates the password, installs the
  management key, turns password SSH off and sets auto-login, verifies
  version, SIP, desktop and password after a reboot, and commits the clone as
  `<goldens_dir>/<template>.qcow2` (owner-only read mode 0400). Rebuilding needs a new
  template name. Unused node goldens are deleted through the exact-name image operation,
  retaining provider routing; an existing backing overlay blocks deletion.
- **Leases** of that template clone a qcow2 overlay into
  `~/virfield/leases/<vm>/storage` (copy-on-write off) and start container
  `virfield-<vm>` with guest SSH published on `0.0.0.0:<port>` (ports
  `port_base … port_base+max-1`; image builds use the next `max`). The lease's
  `ip` is the node address and `ssh.port` its port; `virfield ssh-config`
  writes the port. Virfield's own guest SSH tunnels through the pinned node
  connection to `127.0.0.1:<port>`. A running VM reports SSH only once the
  guest's SSH banner answers, since the port forward accepts TCP earlier.
- **Stop** asks the container to stop for 30 s, kills it only if stop fails,
  removes the container and verifies absence: macOS clones
  can hang at the very end of a shutdown. Release deletes the clone.
- Capacity: the node admits exactly one host lease plus `max` VMs. A VM cannot consume
  the physical-host slot. A fresh post-wake probe must match the requested boot volume. Two VMs booting
  together off a 5400 rpm HDD took about nine minutes to become ready;
  one boots in about two.

### Tart image adoption

A Tart daemon uses one configured `config.json` and private `state_dir`.
Do not run a second manager against its targets or reuse a retired journal. Tart pulls from GHCR need
`runtime.tart.registry_credentials` for `ghcr.io`: Tart always reads
`~/.docker/config.json`, and a stale GHCR entry there is rejected.

```json
"runtime": {
  "provider": "tart",
  "tart": {
    "binary": "/opt/homebrew/bin/tart",
    "host_id": "local",
    "containers": [{"id": "samsung-tart", "path": "/Volumes/Samsung970EVO/virfield-tart", "kind": "external"}],
    "registry_credentials": [{"host": "ghcr.io", "username": "mallexxx", "token_file": "/Users/admin/.virfield-v2/registry-credentials/ghcr-mallexxx.token"}],
    "image": {"packer": "/opt/homebrew/bin/packer", "xcode_archives": "/Users/admin/.virfield-v2/cache/xcode"}
  }
}
```

A Tart template names the container as `location`, `guest_user: "admin"` and
an `image` with a `tart.upstream` digest, exact `macos`/`build`, `xcode` and
`developer-v1`. `image-build` then runs `resolve → build → qualify → stop`;
evidence is `images/<id>/verification.json` with the upstream and recipe
digests. Retry is available for `resolve` and `qualify`; an interrupted
`build` needs `delete` and a new build. Cirrus guests report APFS encryption
at rest with FileVault off; Tart keeps the machine identity on clone, so Tart
qualification accepts it while still requiring FileVault off and unlocked
volumes.


## New installation

Build from current source, then initialize a NEW private deployment:

```sh
virfield init /absolute/new/deployment TEMPLATE VM CONTAINER
```

`init` emits an explicit Tart runtime with the current user's `.tart` storage;
it refuses an existing directory and never rotates a live token. Configure pinned
Tart profiles, approved bases and `runtime.tart.image` (Packer/Xcode cache). The
config must select `tart` or the unaccepted `orchard` adapter explicitly; no
implicit Lume fallback or `image_tools` installer exists. A deprecated `lume_url`
is tolerated in an otherwise valid Tart config solely for migration compatibility.

Install the three binaries `virfield`, `virfieldd`, `virfield-mcp` in deployment
`bin`. Install the native Apple browser alongside them if Apple sign-in is needed,
and register its URL scheme through LaunchServices. The browser is optional for
cached/public artifacts. A stable signing identity and `ai.virfield.virfieldd`
identifier retain Full Disk Access across rebuilds on external storage.

```sh
virfield config-check /absolute/deployment/config.json
python3 deploy/prepare-services.py /absolute/deployment
```

The generator validates private state/config/token paths and executables, then
writes private `launchd/ai.virfield.virfieldd.plist` and `launchd/mcp.json` without
starting services. Deployment and state directories may differ. It refuses an
existing `launchd` directory. Only Virfield is managed; Tart is invoked as a CLI.
Review/register the generated service as the VM-owning user. Do not install a
Lume service. The [service example](../deploy/ai.virfield.virfieldd.plist.example)
is a template, not an installed configuration.

## Service upgrades

Build and verify outside live state. Record the exact source revision, clean/dirty
metadata and binary hashes. Preserve the installed config, credentials, journal
and binaries before replacement. Check for active jobs and leases, drain ongoing
mutations, replace the configured binaries and gracefully restart only the exact
`ai.virfield.virfieldd` service. Do not use another listener as an upgrade.
Verify health, runtime inventory, preserved leases and installed hashes after
restart. New signatures may need privacy grants before external storage works.
Do not claim the upgrade complete from a successful file copy alone.

## CLI and SSH

Use `virfield -help` for command syntax and the installed URL/token file for API
commands. Mutations use one stable idempotency key per logical operation.

```sh
virfield keygen /absolute/new/private/identity
virfield -token-file "$VIRFIELD_HOME/token" -key task-acquire-001 acquire TEMPLATE 3600 /absolute/new/private/identity/id_ed25519.pub
virfield -token-file "$VIRFIELD_HOME/token" job JOB_ID
virfield -token-file "$VIRFIELD_HOME/token" ssh-config LEASE_ID /absolute/new/private/identity
ssh -F /absolute/new/private/identity/config virfield
```

Wait for the job to succeed and the lease to become `ready`. SSH config verifies
the caller key, pins the remote host key and disables inherited agents/passwords.
Never suppress host-key checks. Export artifacts before authorized release.
TTL is 60–86400 seconds; renewal uses a future absolute RFC3339 timestamp within
24 hours and never shortens the existing deadline. Queueing belongs to Broker.

Physical-node workspaces are `~/virfield/leases/<vm>/workspace`, not `~/workspace`.
VM workspaces are `~/workspace`. Physical release revokes the caller key, removes
that workspace and releases sleep holds; it does not reset the shared account or
kill its workloads. Local Tart cannot clone a local golden across `TART_HOME`
containers; do not configure a cross-container fallback for such templates.

### Desktop (R3)

`virfield desktop LEASE_ID [--print]`, Console Desktop and MCP `vm_tunnel` with
`service: desktop` expose a ready VM's Screen Sharing through a loopback tunnel on
the daemon host. The URL contains its lease password; keep it private. Node VM
desktop traffic crosses pinned node SSH to loopback SSH-port+1000. Physical host
leases have no desktop. There is no browser noVNC view.
SSH/desktop tunnels are ephemeral and close on release, expiry, drift or explicit
close. A remote client's localhost is not the daemon host's localhost.

See [remote debugging](REMOTE-DEBUG.md) for generated SwiftPM/LLDB workspaces.
Tart remote-debug acceptance is still pending.

## MCP and remote access

`virfield-mcp` calls the same authenticated API. Help topics `workflows`, `images`,
`operations`, `verification` are embedded and are also `virfield://docs/TOPIC`
resources. Updating source does not update installed help.

Tools: `virfield_help`, `virfield_status`, `vm_acquire`, `vm_lease`, `vm_release`, `vm_renew`,
`virfield_job`, `virfield_events`, `image_catalog`, `image_create`, `image_apple_auth`,
`image_build`, `image_pull`, `image_publish`, `registry_sources`, `registry_resolve`,
`vm_tunnel`, `vm_tunnel_close`.

`image_create`, `image_pull` and `image_publish` use Tart contracts described in
the image topic. Their new implementation has local tests; live factory and
publication acceptance remain recorded separately in Verification.

API binds to loopback. Remote clients need an authenticated TLS proxy, Host
allowlist and separately accepted routing. Scoped REST principals are implemented;
HTTP MCP is operator-only. Neither configuration nor tool discovery proves a
remote workload works.

### Register Codex and Claude Code

Use the actual existing deployment and preserve unrelated server entries:

```sh
codex mcp get virfield
claude mcp get virfield
codex mcp add virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
claude mcp add --transport stdio --scope user virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
```

Register an accepted installed candidate; reconnect after upgrades. Each client
needs help/status discovery plus an authorized acquire → ready → pinned SSH
workload → artifact export → release run. Registration alone is not acceptance.
Claude Desktop configuration is separate from Claude Code.

## Recovery

Inspect the exact job/target and confirm no operation is in flight. Never edit
SQLite, manually mark an image ready or manipulate runtime files to evade the
journal. A lost response uses the same body and idempotency key.

```sh
virfield -token-file "$VIRFIELD_HOME/token" -key inspected-resolution-001 resolve LEASE_ID EXACT_VM_NAME CONFIRM-NO-OPERATION-IN-FLIGHT
```

Operator recovery is absent from MCP. Image recovery uses its separate contract
in [Image pipeline](IMAGE-PIPELINE.md#recovery). Destructive cleanup needs explicit
operator/user authorization and exact-name confirmation.

## Quotas and backups

Tart resource admission counts external VMs and reservations, including full
potential disk growth. A strict explicit clone destination never falls back.
Nodes have separate slots and exclusive host reservations. Node VM budgets reserve
CPU/RAM and full potential disk growth for workers and unfinished image builds.
The default budget is the observed CPU count, RAM minus 4 GiB and a 20 GiB disk
reserve. Override it through `runtime.nodes[].vms.resource_limits` with positive
`cpu`, `memory_bytes` (allocatable budget), `disk_reserve_bytes`; limits are bounded
by observed hardware. `vms.cache_limit_bytes` optionally caps the physical golden
cache plus full potential sizes of pending image builds; zero leaves it uncapped.
No images are pruned automatically. The probe reads qcow2 virtual sizes and free
space on both golden and overlay storage; unavailable metrics refuse admission.
Clone/start/commit recheck after wake. Status exposes separate `node_resources`.
Existing probe records need a successful new observation before VM admission.
These are VM reservations; physical-account workloads are not CPU/RAM sandboxed.
The implementation is in the new source candidate; live node acceptance is pending.

Backup snapshots SQLite, config, the owner-token hash and image credentials into
an owner-only directory; five complete snapshots are retained. VM disks are not
included. Node management keys and cached probes are included; symlinks and oversized
identity files abort the snapshot. External registry/Orchard/principal credentials
referenced by the config need separate operator backup. Restore must preserve image identities, use a new
operator token and start exactly one manager; a fresh restore drill is pending.
Journal retention removes completed released history after 30 days; unfinished
jobs and active leases retain their request keys. Retired idempotency keys may
become new requests after pruning. Migration snapshots are retained separately.
