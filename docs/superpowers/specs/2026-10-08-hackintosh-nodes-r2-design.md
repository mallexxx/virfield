# Remote nodes R2: macOS VMs on the Linux HTPC

Status: in progress (2026-10-08). Builds on R1 (`2026-10-08-remote-nodes-r1-design.md`).

## Goal

Lease disposable macOS 14 (Sonoma) and 15 (Sequoia) VMs with SIP disabled on
the HTPC (i7-6700K, 32 GB, Bazzite 43), beside the R1 bare-metal lease of the
HTPC itself. No sudo on the node, same lease contract as Tart: pinned per-lease
host key, per-lease client key, TTL, release destroys the clone.

## Decisions

- Runtime: rootless Podman with the `dockurr/macos` image pinned by digest
  (QEMU + OpenCore, KVM). `/dev/kvm` and `/dev/net/tun` are usable by the
  node account; networking is passt with `USER_PORTS=22`, and each VM's SSH is
  published on a per-lease host port.
- SIP: OpenCore `csr-active-config` = `0x7F` in a custom `config.plist`
  mounted at `/custom.plist`, also listed in NVRAM `Delete` so the value is
  enforced on every boot. Recovery and `csrutil` are not involved.
- Storage: goldens are read-only qcow2 files on the HDD
  (`/run/media/system/Data/virfield/goldens`, btrfs, 2.7 TB free). Each lease
  is a qcow2 overlay on the SSD (`~/virfield/hackintosh/leases/<vm>`) whose
  backing file is the golden; dockur generates a fresh identity and NVRAM
  per lease. Writes stay on the SSD; untouched blocks are read from the HDD and then
  served from the page cache.
- Goldens are built on the SSD (installs are write-heavy), then compacted to
  the HDD with `qemu-img convert` under a versioned name and made read-only. A
  rebuilt golden gets a new file; an old one is removed only when no overlay
  references it. The HDD is an automount (`nofail`), so goldens are available
  only while it is mounted.
- Install: dockur downloads Apple's recovery image for `VERSION`. Disk Utility
  and the installer are driven over the web VNC viewer, and Setup Assistant
  creates the bootstrap account `admin`/`admin`. After SSH is up,
  Virfield's existing guest scripts apply: guest checks (OS/build, SIP),
  secure (rotate bootstrap password, auto-login), host-key reset, per-lease
  SSH isolation, and the developer recipe's Xcode steps when wanted.

## First build: Sonoma 14.8.9 (2026-10-08)

- Image `dockurr/macos@sha256:08d1bcaa…3d` (v3.12). Recovery download, Disk
  Utility, install and Setup Assistant (bootstrap `admin`/`admin`, no Apple
  ID, analytics and location off, Remote Login on) were driven over noVNC.
  Keys reach noVNC only as key events (no Shift); clicks need one focus click.
- SIP: `0x77` (the pre-Big Sur value) leaves "BaseSystem Verification"
  enabled and `csrutil` reports a custom configuration; `0x7F` gives the
  canonical "System Integrity Protection status: disabled." that Virfield's
  SIP check requires.
- Base contract matches the Tart base images: passwordless sudo for `admin`,
  no sleep or screensaver, automatic updates off, host keys regenerated
  before the final shutdown.
- Golden: `qemu-img convert` of the 100 GB sparse disk to
  `goldens/sonoma-14.8.9-23J631-20261008.qcow2` on the HDD took 2.5 minutes
  (14.9 GB), mode 0444, OpenCore plist stored beside it.
- Clone: a qcow2 overlay created with an absolute backing path, and the
  goldens directory mounted read-only at the same path. dockur skips the
  installer when the disk has data and generates a new identity (serial,
  board, MAC, NVRAM) when the lease folder has none. SSH answered 107 s
  after container start; the overlay was 57 MB after boot; SIP, passwordless
  sudo and macOS version verified in the clone.

## Second build: Sequoia 15.8.1 (2026-10-08)

- The first install phase finished staging but its window froze for over an
  hour (CPU busy, no disk or network I/O). A power cycle booted the staged
  installer and the second phase completed in about 20 minutes. A watcher on
  QEMU's write rate (no writes for 10 minutes) now flags both a finished
  phase and a hang.
- Sequoia's Setup Assistant asks for the transfer choice first ("Set up as
  new") and adds an automatic-update screen ("Only Download Automatically").
- Golden `goldens/sequoia-15.8.1-24H32-20261008.qcow2` (18 GB). A clone in a
  `chattr +C` lease folder booted to SSH in 112 s with a new serial; SIP
  disabled, passwordless sudo and the version verified.

## Implementation notes

- Create lease folders with `chattr +C`: dockur warns about btrfs
  copy-on-write under qcow2, and the warning disappears with it.
- Clones can hang at the very end of a guest shutdown (SSH gone, QEMU busy),
  and the container's SIGTERM does not stop macOS. Stop is therefore: `sync`
  and `shutdown -h now` over SSH, wait for SSH to drop plus a short grace,
  then `podman kill`. Release deletes the clone anyway.
- Per-lease host keys come from Virfield's lease preparation as for Tart; the
  goldens ship freshly generated keys from their final shutdown.
