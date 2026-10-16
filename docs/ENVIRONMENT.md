# Environment checks

Use this guide to check a deployment and qualify the workload you intend to run.
A version in the catalog is metadata; an image marked ready has passed its image
checks. Test your workload on a disposable clone before relying on a profile.

## Check requirements

| Target | Requirements |
| --- | --- |
| Local macOS VM | Apple Silicon macOS, Tart, private configured storage |
| Tart image build | Packer, digest-pinned approved base; verified Xcode archive for developer profiles |
| Physical macOS/Linux node | Pinned SSH, enrolled management key, configured boot environment |
| Linux-hosted macOS lab VM | Rootless Podman/QEMU/OpenCore, KVM/TUN access, pinned container image, bootstrap golden |
| UI testing | Logged-in unlocked console and enabled automation permissions |
| Remote debugging | Matching guest developer profile, host LLDB-DAP and official VS Code extension |

Use Tart plus SSH nodes for deployment. The Orchard adapter is experimental.
Node image profiles do not accept Xcode provisioning fields. Physical hosts
share their configured user account and do not isolate or kill its processes.
HTTP MCP uses the operator principal; scoped REST principals support `lease:own`
and `image:build`. Put remote access behind an authenticated TLS proxy.

## Inspect the running deployment

Read `virfield_status` and confirm fresh verified inventory, the expected runtime,
image fingerprint, storage and capacity. Investigate offline hosts, unknown disk
sizes and missing volume identities before acquiring. Check the exact guest
OS/build, architecture, Xcode and security policy required by your task.

After an upgrade, compare installed hashes with `release.json`, restart the exact
service and reconnect each owning MCP client. Read all four help topics from that
client; replacing a file does not replace an already-running stdio adapter.

## Check a disposable worker

Acquire with a fresh caller key and saved request key. Require successful job and
ready lease, then use generated pinned SSH configuration. Check OS/build, policy,
Xcode and the actual build/test command. Export a result file and compare its hash.
For reboot-dependent work, verify a changed boot identity and repeat native GUI,
SSH and workload checks after reboot. Preserve needed outputs before authorized
release; require confirmed cleanup and revoked tunnels.

For UI work, check the real console and automation permissions. For key storage,
require native GUI and a separate SSH session to perform add/read/data comparison/
delete; an unlocked flag or successful `security` command is insufficient.
See [Operations](OPERATIONS.md#vm-keychain-readiness) for diagnostics.

Factory, registry roundtrip, local transfer and restore are separate operations.
Exercise the path your task requires; one successful clone does not establish
that a backup restores disks or that a registry export is portable. Keep test
journals, credentials and results outside the repository.
