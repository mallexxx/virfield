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
construction additionally requires Packer; developer recipes also require a
verified Xcode XIP cache unless the approved base already provides that Xcode.
`runtime.provider` selects Tart; `runtime.nodes` adds pinned SSH nodes.
Containers have stable IDs, absolute paths, local/external kind and, for removable
storage, a pinned volume UUID. CPU/RAM/disk reserve and VM slots are explicit.
Tart checks directory availability and fresh allocatable capacity with `statfs`
on every refresh. It reuses the verified volume UUID only for the same kernel
filesystem ID, device and mount point; a changed mount needs a new lookup. A
failed or interrupted lookup never substitutes an older identity.
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
        "port_base": 22100, "max": 2, "cpu": 4, "memory_gb": 8, "disk_size": "100G",
        "storage_dir": "/run/media/system/Data/virfield/runtime"}
```

- **Bootstrap goldens** are installed once by hand (see the
  [R2 spec](superpowers/specs/2026-10-08-hackintosh-nodes-r2-design.md)):
  `admin`/`admin`, Remote Login, passwordless sudo, SIP disabled through
  `<golden>.opencore.plist` (`csr-active-config` `0x7F`). They are never
  leased: a clone without a verified image is refused.
- **Secured goldens** are built by Virfield from a template whose image is
  `{"node": {"golden": "<bootstrap name>"}, "macos": …, "build": …,
  "disable_sip": true}` (`POST /api/v1/images/<template>/build`). The job
  clones the bootstrap golden into `<VM storage root>/images/<template>`, publishes
  its SSH on the node loopback only, rotates the password, installs the
  management key, turns password SSH off and sets auto-login, verifies
  version, SIP, desktop and password after a reboot, and commits the clone as
  `<goldens_dir>/<template>.qcow2` (owner-only read mode 0400). Rebuilding needs a new
  template name. Unused node goldens are deleted through the exact-name image operation,
  retaining provider routing; an existing backing overlay blocks deletion.
- **Leases** of that template clone a qcow2 overlay into
  `<VM storage root>/leases/<vm>/storage` (copy-on-write off) and start container
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
  together off a 5400 rpm HDD took about nine minutes before the native keychain
  checks were added; one boots in about two. Node VM preparation has a bounded
  20-minute budget for boot plus credential/keychain verification. The caller's
  lease TTL still includes preparation and is never extended by this budget.

`vms.storage_dir` selects a private root for new VM `leases/` and `images/`; omission
uses `~/virfield`. Physical-host workspaces and shared sleep holds stay in the
node account's home. Set or change it only with no active jobs/VM overlays:
remembered overlays and legacy VM folders block a root change. No files migrate
automatically. Probes measure this root's filesystem together with golden storage.
The HTPC home NVMe currently has 99 GiB free, insufficient for a 100 GiB VM plus
20 GiB reserve; its data HDD has 2.7 TiB free. The new storage-path implementation
and configured HDD root are undergoing acceptance.

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

The native browser reads the private deployment `config.json` to pin its loopback
origin. A separate private `state_dir` is supported. Deployment/config/state must
be owned by the VM user, with no group/other access; config/state symlinks are rejected.

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

The API daemon uses `ProcessType=Standard`. `Background` applies stronger CPU/I/O
limits intended for work not requested by a user, and HTTP requests do not give
it Adaptive XPC priority boosts. See Apple's
[launchd process classifications](https://github.com/apple-oss-distributions/launchd/blob/main/man/launchd.plist.5).
Replacing binaries does not change an already registered job's process class.
An older system LaunchDaemon needs its reviewed plist updated and re-registered
with administrator rights during a fully idle maintenance window, preserving
the label, account, paths and environment. A temporary `taskpolicy` adjustment is
not persistent migration or proof that host contention has been resolved.

## Service upgrades

Build and verify outside live state. Record the exact source revision, clean/dirty
metadata and binary hashes. Preserve the installed config, credentials, journal
and binaries before replacement. Check for active jobs and leases, drain ongoing
mutations, replace the configured binaries and gracefully restart only the exact
`ai.virfield.virfieldd` service. Do not use another listener as an upgrade.
Verify health, runtime inventory, preserved leases and installed hashes after
restart. New signatures may need privacy grants before external storage works.
Do not claim the upgrade complete from a successful file copy alone.
Existing stdio MCP processes retain their old version, tool metadata and embedded
runbooks even after their executable is replaced. Reconnect each owning client
after the upgrade, preserving unrelated clients and active work. A daemon restart
does not replace a client's stdio adapter. Verify the MCP initialize version and
all help topics/resources against the exact packaged source before accepting it.

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

The read-only installed smoke runs from the exact candidate source. Set
`VIRFIELD_LIVE_MCP=1`, `VIRFIELD_LIVE_TOKEN_FILE` to the existing private API token
file, `VIRFIELD_LIVE_MCP_BINARY` to the installed adapter, and
`VIRFIELD_LIVE_MCP_VERSION` to `release.json.version` without its leading `v`, then
run `go test ./internal/mcpadapter -run '^TestLiveInstalledMCP$' -count=1 -v`.
It checks both HTTP and a newly started stdio adapter. Separately inspect
help/status in the actual already-connected client: a fresh process passing does
not attest an older process still serving a different session.

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
Node QEMU disks use `cache=writeback` with threaded I/O so concurrent cold boots
can share the host page cache on HDD storage. Guest cache flushes remain enabled;
this is the standard QEMU writeback mode ([QEMU cache options](https://www.qemu.org/docs/master/system/invocation.html)).
New node image conversion explicitly sets the OCI process umask to `0077`;
the temporary qcow2 is private from creation and becomes mode `0400` before
publication. Existing staging files are not overwritten; exact-name image
deletion also removes that image's interrupted conversion file.
Quotas and the private HDD root are installed; two concurrent reservations and
two Sonoma native GUI/SSH reboot cycles passed on the dated candidate in
[Verification](VERIFICATION.md). The complete current node workflow matrix
remains open; consult that record for the latest reproduced failure and evidence.

Backup snapshots SQLite, config, the owner-token hash and image credentials into
an owner-only directory; five complete snapshots are retained. VM disks are not
included. Node management keys and cached probes are included; symlinks and oversized
identity files abort the snapshot. External registry/Orchard/principal credentials
referenced by the config need separate operator backup. Restore must preserve image identities, use a new
operator token and start exactly one manager. The 2026-10-14 offline drill and
2026-10-15 live controller-state restore passed, including a rotated token, exact
MCP and two native Monterey reboot/lease cycles before return to original state.
This does not back up or restore VM disks.
Journal retention removes completed released history after 30 days; unfinished
jobs and active leases retain their request keys. Retired idempotency keys may
become new requests after pruning. Migration snapshots are retained separately.

## VM keychain readiness

A temporarily unavailable host, or a running VM whose address is currently
unknown, does not prove `vm_drift`. The controller preserves the ready lease's
verified identity, pauses new tunnels with retryable `backend_unavailable`, and
re-evaluates after inventory recovers. Established tunnel streams remain connected
to their verified peer. Cached listeners reject new connections while the host,
address or inventory freshness is unverified; they resume on the same port after
recovery. Endpoints are checked before and after each new upstream dial. Release,
expiry and verified identity/state changes still revoke the tunnels. Two verified observations of an absent or
stopped VM or a changed known endpoint still require release. Never repair a real
drift by rewriting stored addresses or disabling SSH pins.

The candidate preserves/rekeys the login default and prepares an additional
owner-only keychain named for each image/lease,
an Aqua LaunchAgent (`ai.virfield.guest-keychain`) and a VM-only SSH command bridge
(`/etc/ssh/sshd_config.d/001-virfield-keychain.conf`). Guest state is private under
`~/.virfield-keychain`. Worker preparation first requires the inherited Finder
desktop to settle before rotating account/autologin credentials, matching image
qualification. This read-only preflight leaves credentials untouched on failure.
Its bounded private `leases/<id>/desktop.log` retains the wait deadline, command
budget, observation duration, partial output and original error, without creating
worker credentials or overwriting the golden's diagnostics.
Credential mutation failures retain bounded private stdout/stderr even after a
deadline, plus a fixed failure class and secret-free timestamped phases in
`leases/<id>/provision.log`. No command is automatically replayed after a timeout.
Aqua unlock is performed by the owned GUI agent. `RunAtLoad` can remain registered
with zero runs and a speculative pending spawn after login. Qualification and
post-reboot verification demand this agent without `-k`, leaving any running
startup intact. The worker SSH bridge waits for Aqua registration and current
scope/boot startup proof before executing the original command, then unlocks in
its own Security audit session. This wait is bounded and does not consume the
original command's stdin. Image bootstrap SSH is exempt so it can prepare the GUI.
Only a completed automatic startup in the same scope, boot and Aqua audit session
can be reused; explicit nonce-bearing native requests always run again.
The inherited login keychain's items are retained and its password is rotated to
match the VM. It remains the default and first in the search list across boots:
loginwindow unlocks it during automatic login, before early OS clients start.
A custom scoped default can leave Spotlight password dialogs queued before the
owned Aqua agent starts, even if that agent subsequently unlocks it.
Native SecKeychainUnlock/ChangePassword authenticates each old-password candidate
with user interaction disabled in that same caller. A wrong password returns
`errSecAuthFailed`; only that error permits a fallback. The GUI `security` CLI can
hang in a password dialog and is not used for this authentication. Secrets are
read from the private identity file, not command arguments.
The preparer does not deliberately lock the login keychain; an artificial lock can leave an
OS client's password dialog open even after a successful unlock. Successful rekey
is followed by native WhenUnlocked add/read/delete with the new password. A
desktop overlay cannot select another password, and failed I/O after successful
authentication fails preparation without trying another secret. Full readiness
separately requires an available Aqua window snapshot with no visible Keyboard
Setup Assistant or SecurityAgent window. An already queued OS password dialog is
not dismissed by native I/O success; preserve diagnostics and fail readiness.
It also requires the current, logged-in console to be unlocked; Finder and an
Aqua audit session can remain alive behind the lock screen. Automation disables
both general and per-host screensaver idle time. Account/autologin preparation
clears and verifies `autoLoginUserScreenLocked`; this flag otherwise restores an
earlier idle lock after reboot even when system/display sleep are disabled.
The read-only inherited desktop preflight requires an unlocked console before
credential rotation. It never types a password into an existing lock screen.
The fixed QEMU USB and Apple PS/2 ANSI keyboard mappings preserve other devices.
Only the virtual Keyboard Setup Assistant may be closed; SecurityAgent is never
killed or answered by this agent.

Tart clones and factory recipes request `1920x1080px` (72 PPI), independent of the host Retina scale. Point units, including an omitted unit, can leave Monterey at 3840×2160 without a 1920×1080 mode. The actual 1920×1080 desktop is selected and verified inside Aqua before native keychain acceptance. Shared physical nodes are not
modified by this VM preparation policy.

Console, automatic startup and fresh native-result observations share one
command bound; observing a later phase does not restart that budget. Initial
`ensure` retains five minutes. Post-reboot `verify` allows ten minutes because
SSH can precede the cold HDD Aqua login/agent by minutes; the caller's original
operation/lease deadline still caps it. Cold agents have produced valid proof
just after both an earlier 120-second guard and a five-minute verification bound.
Native scope/boot/nonce and I/O checks remain mandatory.
Time already spent in the worker SSH GUI gate is deducted from the native command
budget, and an exhausted budget fails before another mutation phase. An SSH
banner alone does not prove that the post-reboot GUI startup has completed.
Before worker readiness, a separate bounded probe reads a complete RFB greeting
from guest localhost:5900 through authenticated, pinned guest SSH. This starts and
verifies Screen Sharing itself; launchd owning a LISTEN socket is insufficient.
It does not change firewall policy or weaken authentication.
The worker Aqua agent also reads the real greeting before publishing native
readiness on each login/reboot; image qualification skips this worker service.

If `guest_keychain_failed` occurs, preserve the operation/lease ID and inspect the
owner-only guest `~/.virfield-keychain/agent.log` and host provisioning diagnostics.
The SSH preparer now captures bounded GUI/SSH results and agent/display logs into
private `state_dir/leases/LEASE_ID/keychain.log` (or `images/IMAGE_ID/keychain.log`)
before closing its provisioning connection. It omits the identity/password file
and keychain contents. A failed GUI installation can make the new SSH bridge
unavailable, so collect this host log before releasing the owned fixture: cleanup
removes its private directory. Public API errors/events never contain these logs.
Do not mark ready on a CLI unlock exit code: native application-style key storage
must work in both contexts. Do not replace a keychain on an active user's lease or
reset application databases; prepare a new disposable lease after deployment.
The lease wait ended and signed candidates were installed through the existing
launchd service. Keep source, installed-package and already-connected-client
acceptance separate; [Verification](VERIFICATION.md) records their exact scope.
