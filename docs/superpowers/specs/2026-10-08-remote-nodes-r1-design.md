# Remote nodes R1 — bare-metal SSH provider — design

Date: 2026-10-08. Status: approved. Implements the first slice of Phase 2 in
[Virfield 3.0 implementation plan](../../VIRFIELD-3-PLAN.md#phase-2--node-agent-physical-hosts-and-access).

## Goal

Lease two physical machines on the home LAN beside the local Tart runtime,
through the existing lease API and per-lease SSH isolation:

| Node | Facts (probed 2026-10-08) | Lease target |
|---|---|---|
| `mbp-2014` | MacBookPro11,1, i5-4278U, 16 GB, x86_64, `admin@192.168.1.79`, host key ED25519 `SHA256:s7NpMEeAGThaC2X9U8mJOX+NZc0za11uZ50boh1z4h8`; volumes Catalina, Big Sur, Monterey (booted, 12.6.8/21G725), Ventura, BOOTCAMP; sudo needs a password; no Ethernet | Whatever macOS is booted |
| `htpc` | Bazzite 43 (Kinoite, deck), 8 CPUs, 31 GB, `bazzite@192.168.1.86`, host key ED25519 `SHA256:fdOuV+GUtztFZJ8AAjKLnbd0gbPms/tfFI8YKSuu064`; Wake-on-LAN MAC `B0:6E:BF:60:6D:64` (woke in ~16 s); Podman present; sudo needs a password; VT-x disabled in UEFI (no `/dev/kvm`) | Linux host with Podman |

## Decisions

- **SSH-first, agent later.** Control runs fixed commands over pinned SSH. A
  later `virfield-node` agent implements the same provider seam.
- **No sudo on nodes.** Every node operation runs as the node account.
- **Lease what is booted.** The MacBook exposes its booted volume; boot
  switching is out of scope.
- **Dynamic availability.** An unreachable node is an unavailable host with a
  reason (`offline`); it never fails the whole inventory. A node with
  Wake-on-LAN is admitted while asleep and woken at start.
- **Physical hosts are not disposable.** Release revokes the lease key, stops
  the lease's sleep inhibitor and deletes the lease workspace. It does not
  restore the host. Processes started under the shared account are not killed;
  this is reported as the bare-metal cleanup capability.

## Components

1. **`internal/wol`** — magic-packet sender extracted from the Orchard client;
   both Orchard and bare metal use it.
2. **`internal/baremetal`** — `provider.Runtime` (+ `Named`, `PowerController`,
   `InventorySeed`) for SSH nodes, plus a lease preparer.
   - *Inventory*: per node, a bounded SSH probe (management key, pinned host
     key) reads the boot volume name, OS version/build, CPU, memory and the
     lease markers under `~/virfield/leases`. It reports one host, one storage
     container (ID = node ID), one pseudo-golden VM named after the booted
     volume (`Monterey`, `bazzite`), state `stopped`, resources = the host, and
     one VM per lease marker (`running`/`stopped`, IP = node address,
     `ssh_available` when reachable). Unreachable → host `unavailable`,
     reason `offline`; known lease VMs keep their last state with
     `ssh_available = false`.
   - *Clone* writes the lease marker (`stopped`); *Start* wakes the node if
     needed, writes `running` and starts a sleep inhibitor (`caffeinate -s` on
     macOS, `systemd-inhibit --what=sleep:idle` on Linux) recorded by PID;
     *Stop* stops the inhibitor and writes `stopped`; *Delete* removes the
     lease's `authorized_keys` entries, stops the inhibitor and deletes the
     marker and workspace. Every step is idempotent and only touches
     `~/virfield/leases/<vm>` and lines tagged `virfield-lease-<vm>`.
   - *Prepare* appends the client public key tagged `virfield-lease-<vm>` to
     `~/.ssh/authorized_keys`, creates `~/virfield/leases/<vm>/workspace`,
     verifies the exact line, and returns the node user, port 22 and the
     pinned host key.
3. **Composite runtime (`internal/fleet`)** — holds the primary runtime (Tart)
   and named secondaries. Inventory merges hosts, containers and VMs;
   lifecycle calls route by `template.Provider` / `lease.Provider`; wake routes
   to the runtime that owns the host; the provider name stays the primary's.
   A composite preparer routes `Prepare` the same way.
4. **Controller** — leases record `template.Provider` (empty = primary);
   bare-metal templates clone at their own location; capacity is per host
   (limit = host `max_vms`, used = active leases and running VMs on that
   host), so a bare-metal lease never consumes a Tart slot.
5. **Config** — `runtime.nodes[]`: `id`, `name`, `address`, `user`, `host_key`
   (authorized-keys format), `os` (`macOS`|`linux`), `arch`, optional
   `wake_on_lan {mac, broadcast}`. Templates use `provider: "baremetal"`,
   `location: <node id>`, `name: <booted volume>`, `guest_user`.
6. **Enrollment** — Virfield generates its node management key in
   `state_dir/nodes/management` (0600). Its public key is appended once to each
   node account's `authorized_keys` (each MacBook volume has its own home and
   is enrolled when booted).

## Templates

| ID | Location | Name (booted volume) |
|---|---|---|
| `mbp2014-monterey` | `mbp-2014` | `Monterey` |
| `mbp2014-big-sur` | `mbp-2014` | `Big-Sur` |
| `mbp2014-catalina` | `mbp-2014` | `Catalina` |
| `mbp2014-ventura` | `mbp-2014` | `Ventura` |
| `htpc-bazzite` | `htpc` | `bazzite` |

Volume names are normalized to `ValidName` (spaces → `-`).

## Testing

- Unit: WoL packet; node probe parsing; lease marker and `authorized_keys`
  scripts (bash -n, no untagged line touched); offline node does not fail the
  inventory; composite merge and routing; per-host capacity (a bare-metal lease
  leaves Tart capacity untouched); preparer routing; config validation.
- Live: enroll both nodes; acquire `mbp2014-monterey` and `htpc-bazzite`
  concurrently with two Tart leases; SSH with the lease key and pinned host
  key; workspace present; release revokes the key and removes the workspace;
  HTPC asleep → woken by the lease; offline MacBook → template unavailable.

## Out of scope

Boot-volume switching; node-agent; libvirt/OpenCore Hackintosh on the HTPC
(needs VT-x enabled in UEFI; R2); desktop/noVNC gateway (R3); Wi-Fi wake for
the MacBook.
