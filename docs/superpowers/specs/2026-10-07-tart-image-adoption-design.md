> **Reconciled status — 2026-10-14:** This is a dated design/execution record.
> It is not a current deployment runbook or live task tracker. See
> [current delivery state](../../VIRFIELD-3-PLAN.md) and
> [current acceptance](../../VERIFICATION.md); old paths/statuses and unchecked
> example steps below describe their original execution context.

# Tart image adoption — design

Date: 2026-10-07. Status: approved direction; implements Phase 1 of
[Virfield 3.0 implementation plan](../../VIRFIELD-3-PLAN.md) and the Tart path
in [compositional platform plan](../../COMPOSITIONAL-PLATFORM.md).

## Goal

Produce qualified, digest-pinned Tart goldens and pass two disposable Tart
lease clones of each through the existing lease API with per-lease SSH
isolation:

| Profile | Upstream (pinned) | Guest | Xcode | SIP |
|---|---|---|---|---|
| `ventura-xcode-15.2` | `ghcr.io/cirruslabs/macos-ventura-base@sha256:9fb387bb987cdda942fbc27118296ed8e16a3359a672c95c6d5ac960165ef1c5` | 13.7.4 (22H420) | 15.2 (15C500b) | disabled |
| `monterey-xcode-13.4.1` | `ghcr.io/cirruslabs/macos-monterey-vanilla@sha256:4bc904cf8f2447dfa6e6fb9a5eb70eba14196dd0f4236d30fc92aff9c2391842` | 12.7.6 (21H1320) | 13.4.1 (13F100) | enabled |

Both use `developer-v1`. Guiding rule: reuse Tart, Cirrus images and Packer's
Tart plugin as far as possible; Virfield keeps only what upstream lacks. Lume is
not extended or generalized; its removal is Phase 2.

## Decisions and verified facts

| Topic | Decision / fact (verified 2026-10-07 unless noted) |
|---|---|
| Coexistence with live Lume service | **Tart window**: the same launchd service switches between a Lume config and a Tart config with **separate state dirs**; swap only when no Lume lease is active. Rollback is config swap + restart. |
| Storage | Tart container on `/Volumes/Samsung970EVO/virfield-tart` (323 GB free); `/` has too little room for two goldens. |
| Ventura base | Ships 13.7.4 (22H420), SIP **disabled**, `admin`/`admin`, passwordless sudo, autologin `admin`, Finder desktop, Tart Guest Agent. 50 GB disk, 19 GB free. No Ventura Xcode 15.2 image exists upstream. |
| Monterey base | `macos-monterey-xcode` and `macos-monterey-base` use the legacy `tart.disk.v1` layer format that Tart 2.40 refuses (`disk chunks must start with a disk.v2 base`); only `macos-monterey-vanilla` is usable. 12.7.6 (21H1320) is the final Monterey release; it ships SIP enabled, `admin`/`admin`, passwordless sudo, autologin and Finder, no Tart Guest Agent, 41 GB disk with 22 GB free. |
| Clone identity | `tart clone` keeps the ECID (machine identity) for OCI→local and local→local clones; local clones get a new MAC. Cirrus guests report APFS `Encryption=true` with FileVault off, so Tart profiles allow encryption at rest (FileVault off and unlocked volumes are still required). Concurrent same-ECID clones are proven in live acceptance. |
| Image construction | Packer 1.15.2 with `github.com/cirruslabs/tart` plugin pinned `= 1.21.0`. |
| Xcode transport | Packer `file` provisioner uploads the cached, sha1-verified XIP over SSH; the guest checks sha1, Apple signature and version, then expands it. Same path for both profiles (Monterey has no VirtioFS). Disk grown to 120 GB by the plugin. |
| SIP | No Recovery step: the profile states the expected SIP state and qualification verifies it. A profile whose upstream SIP state differs fails qualification. |
| Golden storage | Local stopped Tart VM; leases clone it copy-on-write. GHCR push is a follow-up after acceptance. |
| Registry auth | The host's `~/.docker/config.json` GHCR credential is rejected by GHCR, and Tart always reads it. Tart `registry_credentials` (`TART_REGISTRY_*`, higher precedence) with the existing `personal-ghcr` token are required for pulls. |

## Components

1. **Recipe** — `internal/tartimage/recipe/developer.pkr.hcl` plus
   `scripts/*.sh`, embedded with `go:embed`. Recipe digest = sha256 over the
   sorted embedded files. `source "tart-cli"`: `vm_base_name` = pinned
   upstream, `vm_name` = template VM name, `disk_size_gb = 120`, `admin`/
   `admin` SSH, `headless = true`. Provisioners: assert OS version/build,
   SIP state, sudo and free space (fail fast); upload XIP; verify and install
   Xcode; Xcode post-install (`xcode-select`, license, `-runFirstLaunch`,
   DevToolsSecurity, `_developer`, NFS dyld export for DeviceSupport).
2. **`internal/tartimage`** — implements `control.ImageBuilder` for Tart
   profiles. Runs `packer init`/`packer build` as subprocesses with
   `TART_HOME`, a private plugin dir, `TART_REGISTRY_*` for the upstream host,
   private per-job logs; boots and stops VMs through the existing Tart
   runtime client.
3. **`internal/qualify`** — guest checks moved out of `internal/images` and
   made runtime-agnostic and user-parameterized: OS version/build, SIP state,
   desktop wait, disk policy, password persistence, Xcode bundle/signature/
   version/first-launch/compile probe, host DeviceSupport preparation.
   `internal/images` calls the moved functions with user `lume`, so Lume
   behavior is unchanged.
4. **Domain/config** — `ImageProfile.Tart` (`{"upstream": "<ghcr ref>@sha256:…"}`)
   as a third source alternative to IPSW and GHCR-Lume; `macos`, `build` and
   `xcode` required. `runtime.tart.image` config: Packer binary and Xcode
   archive directory.
5. **Controller** — a Tart phase map selected by `profile.Tart != nil`:
   `resolve → build → qualify → stop`, then the existing `image_ready`
   promotion. The existing collision check guards `build`; restart during any
   dispatched step yields `image_outcome_unknown` (never replayed); operator
   `retry` is allowed for `resolve` and `qualify` only.
6. **Guest identity and leases** — done in stage 1.

## Data flow

```text
resolve  profile has a Tart source; container known; XIP present and sha1
         verified; recipe written to the private job dir; packer init
build    packer build → VM <template name> in the container (stopped)
qualify  tart run; SSH admin/admin bootstrap; desktop; SecureImage; reboot;
         reconnect with pinned host key; qualify checks; DeviceSupport;
         verification.json; clean guest shutdown
stop     confirm stopped → controller promotes image_ready
```

Lease flow: clone local golden (same container) → `tart run` → IP →
`LeasePreparer` as `admin` → `ready` → release → stop + delete.

## Failure handling

- Subprocess or check failure → job `needs_attention` with a stable code and
  the private log path; no automatic retry.
- Packer cleans up its VM on build failure; an interrupted build leaves the VM
  for the existing operator `delete` recovery.
- Qualification never weakens a check to pass.

## Testing

- Unit: profile validation; recipe embedding/digest; `tartimage` commands and
  environment via a fake runner; step sequencing with fake runtime and
  fake guest; `qualify` checks against canned outputs (moved tests keep
  passing); controller Tart phases and recovery.
- `make check` green; Lume tests unchanged.
- Live (stage 3, Tart window): build both profiles; two concurrent leases per
  profile with distinct host keys, rejection of the golden key and the other
  lease's key, `xcodebuild -version`, SIP state, Swift compile, artifact
  export; release and confirm deletion; record in `docs/VERIFICATION.md`.

## Delivery order

1. Guest identity + Tart lease SSH isolation — **done** (`cd1f18d`…`a50c261`).
2. Adoption pipeline — profile/config, `qualify` extraction, recipe,
   `tartimage`, controller phases, wiring.
3. Live acceptance in a Tart window.

## Out of scope

GHCR push of derived goldens; deleting Lume code; Orchard; mixed-provider
routing; node-agent and bare-metal hosts; a Recovery/SIP mutation step.
