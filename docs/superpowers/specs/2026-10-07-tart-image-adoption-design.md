# Tart image adoption — design

Date: 2026-10-07. Status: approved direction; implements Phase 1 of
[Virfield 3.0 implementation plan](../../VIRFIELD-3-PLAN.md) and the Tart path
in [compositional platform plan](../../COMPOSITIONAL-PLATFORM.md).

## Goal

Produce one qualified, digest-pinned Tart golden — Ventura (whatever build the
pinned Cirrus base ships, at least 13.5) with Xcode 15.2 (15C500b), SIP
disabled, `developer-v1` — and pass two disposable Tart lease clones through the
existing lease API with per-lease SSH isolation.

Guiding rule: reuse Tart, Cirrus images and Packer's Tart plugin as far as
possible; Virfield keeps only what upstream lacks. Lume is not extended or
generalized; its removal is Phase 2.

## Decisions

| Topic | Decision |
|---|---|
| Coexistence with live Lume service | **Tart window**: the same launchd service is switched between a Lume config and a Tart config with **separate state dirs**; swap only when no Lume lease is active. Rollback is config swap + restart. No mixed routing, no second daemon. |
| Upstream base | `ghcr.io/cirruslabs/macos-ventura-base@sha256:9fb387bb987cdda942fbc27118296ed8e16a3359a672c95c6d5ac960165ef1c5` (uploaded 2025-09-20, ~22.6 GB compressed, 50 GB raw disk). Cirrus publishes no Ventura Xcode 15.2 image. |
| Profile | Base OS build, Xcode 15.2, SIP disabled, `developer-v1`. |
| Golden storage | Local stopped Tart VM in a Tart container; leases clone it copy-on-write. GHCR push is a follow-up after acceptance. |
| Image construction | Packer with the `github.com/cirruslabs/tart` plugin (pinned version, ≥ 1.12.0), recipes derived from Cirrus `macos-image-templates` (MIT). |

## Components

1. **Recipes** — `internal/tartimage/recipes/*.pkr.hcl` plus provisioner
   scripts, embedded with `go:embed`. The recipe digest is the sha256 over the
   sorted embedded files. Two recipes:
   - `developer-xcode.pkr.hcl`: `source "tart-cli"` with `vm_base_name` =
     pinned upstream, `vm_name` = build VM, `ssh_username/password = admin`,
     `headless = true`, `run_extra_args = ["--dir=xcode:<xip dir>:ro"]`.
     Provisioners, in order: assert `sw_vers` equals the profile (fail fast);
     extract the shared Xcode XIP and install it; Xcode post-install
     (`xcode-select`, license, `-runFirstLaunch`, DevToolsSecurity,
     `_developer`); Virfield security policy (`security.sh`, parameterized for
     the guest user); clean shutdown.
   - `disable-sip.pkr.hcl`: Cirrus's pattern — `vm_base_name` = the build VM,
     `recovery = true`, `communicator = "none"`, VNC `boot_command` running
     `csrutil disable` with the bootstrap password, then `halt`.
2. **`internal/tartimage`** — runs `packer init` (into a plugin dir under the
   state dir) and `packer build` as subprocesses with `TART_HOME` set to the
   container path, private per-job logs and `outputLimit`-style capture; plus
   `tart rename` / `tart delete` for build VMs. All commands go through a
   runner interface so tests use fakes.
3. **`internal/qualify`** — Virfield's existing guest checks extracted from
   `internal/images` into runtime-agnostic functions over an SSH executor with
   the guest user as a parameter: `sw_vers` version/build, `csrutil status`,
   Xcode version/build, Apple code signature, `xcodebuild -version`,
   `xcode-select`, first-launch status, swiftc compile probe, DevToolsSecurity,
   `_developer` membership, disk policy, security-policy verify, desktop
   (Finder + `.AppleSetupDone`). Writes `verification.json`; its sha256 is the
   evidence digest.
4. **Domain/config** — `ImageProfile` gains a `Tart` source
   (`{"upstream": "<ref>@sha256:…", "recipe": "developer-xcode"}`) as a third
   alternative to IPSW and GHCR; validation requires a digest-pinned upstream
   on `ghcr.io` and a known recipe. `macos` and `build` stay required
   (the first provisioner enforces them). Templates set `provider: "tart"` and
   `guest_user: "admin"`.
5. **Controller** — a Tart image job (`image_build` kind, Tart phase set)
   registered only when the runtime is Tart. Image readiness keeps the existing
   `image_ready` lease record; `ImageManifest` becomes
   `sha256(upstream digest, recipe digest, evidence digest)` for Tart profiles.
6. **Guest identity and leases** — `guestssh` and the shared scripts take the
   guest user/home from the template instead of the hardcoded `lume`.
   `LeasePreparer` is wired for Tart, so Tart leases become `ready` only after
   per-lease key, password and host-key rotation and old-key rejection.

## Data flow

```text
resolve  validate profile, upstream digest, recipe digest, Xcode XIP present
         in the host cache (sha1 verified), reserve one VM slot
build    packer build developer-xcode → VM vf-build-<job> (stopped)
sip      packer build disable-sip on vf-build-<job> → stopped, SIP off
qualify  tart run vf-build-<job>; SSH admin/admin bootstrap; SecureImage
         (management key, rotated password, autologin, password SSH off);
         reboot; reconnect with pinned host key; run internal/qualify;
         write evidence; clean shutdown
promote  tart rename vf-build-<job> → template name; record image_ready with
         the derived ImageManifest; release the VM slot
```

The build VM never carries the golden name until qualification succeeds, so a
VM with the golden name is always qualified.

Lease flow is unchanged apart from preparation: clone the local golden (same
container) → `tart run` → IP → `LeasePreparer` (template guest user) → `ready`
→ release → `tart stop` + `tart delete`.

## Failure handling

- Any subprocess failure → job `needs_attention` with a stable code
  (`image_command_failed`, `guest_build_mismatch`, `sip_verification_failed`,
  …) and the private log path; no automatic retry of a non-idempotent phase.
- Daemon restart while `build`/`sip`/`qualify` is in flight → the outcome is
  unknown; the job moves to `needs_attention` (`outcome_unknown`). Recovery
  stops and deletes `vf-build-<job>` (identified by journal + name prefix,
  never a golden) and restarts from `build`.
- A golden name collision at `promote` → `image_exists`; nothing is
  overwritten.
- Qualification never weakens a check to pass; a mismatch is a failed image.

## Testing

- Unit: recipe embedding and digest stability; `tartimage` command lines and
  environment (`TART_HOME`, plugin dir, `--dir` argument) via a fake runner;
  `qualify` checks against a fake SSH executor (ported from existing verify
  tests, including mismatch cases); profile/config validation; controller
  phase transitions and restart reconciliation with fake builders; `guestssh`
  user parameterization (no `lume` literal reachable for a Tart template).
- `make check` stays green; Lume tests stay unchanged.
- Live acceptance (Tart window, recorded in `docs/VERIFICATION.md`): run the
  adoption job once; acquire two leases concurrently; for each verify distinct
  pinned host keys, rejection of the golden's management key and of the other
  lease's key, `xcodebuild -version` = 15.2/15C500b, `csrutil` disabled, a
  Swift compile, SCP artifact export; release both and confirm deletion while
  the golden stays stopped and unchanged.

## Delivery order

1. **Guest identity + Tart lease SSH isolation** — parameterize the guest
   user/home; wire `LeasePreparer` for Tart. No live risk.
2. **Adoption pipeline** — profile/config, recipes, `tartimage`, `qualify`,
   controller job.
3. **Live acceptance** — Tart config with its own state dir, first adoption
   run, two-clone acceptance, verification record.

## Out of scope

GHCR push of the derived golden and Tart media types in the registry client;
deleting Lume code; Orchard; mixed-provider routing; node-agent and bare-metal
hosts.
