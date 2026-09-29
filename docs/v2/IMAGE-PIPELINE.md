# Image Manager

Image preparation is a durable job in `virfieldd`. CLI, UI and MCP submit the same
API request. No client invokes Lume directly. Broker/Runner execution and the
installation of Xcode/provider binaries are separate from this base-image build.
The verified security policy here is SIP only; legacy Gatekeeper/AMFI/TCC
provisioning is not silently applied or claimed as tested.

## Profile and dependencies

Add `image` to an existing configured template. It pins an Apple HTTPS URL,
SHA-256, exact byte size, expected guest build and guest SIP policy. The request
contains only the template ID; callers cannot supply URLs, host paths, scripts,
credentials or CLI arguments.

```json
{
  "id": "macos27",
  "name": "macos-27-golden",
  "location": "home",
  "image": {
    "url": "https://updates.cdn-apple.com/2026FallFCS/afcfc88e-bbe6-44bf-a5da-07c56eebc06c/UniversalMac_27.0_26A428_Restore.ipsw",
    "sha256": "2a5d3c695d501022b7fad9adaffcf2627bcb867d993fb5662dcd41bac99a2836",
    "size": 26626436228,
    "build": "26A428",
    "disable_sip": true
  }
}
```

Set the top-level `image_tools` config to absolute operator-controlled paths:

```json
{
  "lume": "/opt/homebrew/bin/lume",
  "python": "/absolute/private/tools/vnc/bin/python",
  "tesseract": "/opt/homebrew/bin/tesseract",
  "vnc_bin": "/absolute/private/tools/vnc/bin"
}
```

The executor is pinned to Lume 0.5.3 and macOS build 26A428. Other builds are
rejected before any image operation until their recipe is implemented and tested. Install `vncdotool==1.3.0` into an isolated
Python environment and Tesseract before starting a build. The tested Python
3.14.4 package set is pinned in `deploy/image-tools-requirements.txt`; Tesseract
5.5.2 was used for live acceptance. The daemon never
installs host dependencies itself. `assistant.py` is a versioned embedded guest
VNC finisher for macOS 27, derived from the existing Virfield recipe; it cannot
manage VM lifecycle. Lume's native `setup` handles offline patching. Recovery
uses one owned `lume run --recovery-mode true` child plus `recovery27.py`: the
native 0.5.3 SIP navigation was observed to open Time Machine on this build.
The versioned driver verifies Options, English, Utilities, Terminal and each
authentication prompt before input. It accepts only an explicit successful SIP
policy response, requests guest shutdown and waits for fresh stopped inventory.
Normal-boot `csrutil status` is still the mandatory final policy check. Unknown
UI screens fail with private diagnostic screenshots. The finisher waits
for boot frames, reconnects for full VNC framebuffers, recognizes the final
button separately from the wallpaper and delegates the final desktop
postcondition to SSH. Each attempt retains its own screenshot directory.

The first implementation fixes resources at 4 CPUs, 8 GiB RAM, 80 GiB sparse
disk, 1920×1080, NAT, no host shared folders. Changing the recipe requires a code
change and live validation; this is not an arbitrary command runner.

## Build and inspect

```sh
./bin/virfield -token-file /absolute/state/token -key build-macos27-001 image-build macos27
./bin/virfield -token-file /absolute/state/token job JOB_ID
```

A build requires no active leases or running VMs and an absent target name.
It holds maintenance admission until ready or explicitly cleaned up. Image
records have no lease expiry. A successful stopped image consumes no VM slot.

The persisted stages are download → create → setup → assistant → SIP → verify
→ stop → ready. Each external effect has a dispatched checkpoint and a separate
completion event. The manifest is copied into the accepted job, so changing
configuration during a restart cannot change an in-progress restore image.

The download is real HTTPS from Apple, with no redirects. Transient network failures get at most three range-resume attempts. A graceful
daemon shutdown pauses a download for automatic resume on restart. Partial downloads use
Range with validated Content-Range. Only exact size plus a full SHA-256 permits
atomic cache promotion. Disk-space checks run before download and installation.
A cache hit is also hashed before use. Progress is stored every five seconds;
API reads remain fast while the worker performs network or VM operations.

Lume subprocesses have fixed argv, bounded private logs, context deadlines and
an owned process group. Cancellation never uses `pkill`, process-name searches,
or kills the Lume service. No raw process output or VNC password enters events.

Guest provisioning uses Go SSH, a pinned guest host key and a `VirtualMac`
hardware check before privileged operations. The documented `lume/lume`
bootstrap is used only for a newly installed image. Verification replaces it
with generated image-specific credentials, disables SSH password authentication
and checks the effective `sshd -T` configuration,
creates a VM-local workspace, and checks guest build, canonical SIP status and
Finder after a further reboot. Credentials remain in private 0600 files under
the private state directory; they are never returned by status/job/events.
**Image credentials are not yet per-lease Broker credentials.** Per-clone key
rotation and credential delivery remain a separate release gate.

## Recovery and deletion

An interrupted VM mutation is quarantined and holds admission. A completed
checkpoint can continue after restart; an unconfirmed create/setup cannot be
replayed. Download bytes can safely resume, but a failed download can also be retried explicitly. VM mutation recovery requires an
operator to inspect the exact VM and confirm that no operation remains in flight.

```sh
./bin/virfield -token-file /absolute/state/token -key inspected-image-retry-001 \
  image-recover IMAGE_RECORD_ID EXACT_VM_NAME retry CONFIRM-NO-OPERATION-IN-FLIGHT
```

`retry` is limited to download, assistant, SIP and verify at their persisted
failure stage. It cannot skip verification or change the VM identity. A failed
create/setup requires inspection and `delete`, followed by a new build request.

```sh
./bin/virfield -token-file /absolute/state/token -key inspected-image-delete-001 \
  image-recover IMAGE_RECORD_ID EXACT_VM_NAME delete CONFIRM-NO-OPERATION-IN-FLIGHT
```

Deleting a configured stopped image is a separate explicit operation; ordinary
lease cleanup never deletes templates:

```sh
./bin/virfield -token-file /absolute/state/token -key delete-macos27-001 \
  image-delete macos27 macos-27-golden
```

The manager rejects deletion while the image has active leases or jobs. The UI
requires typing the exact VM name. Image deletion and recovery are deliberately
absent from MCP. Deletion releases the record only after fresh inventory confirms
absence. Cache media and audit history are retained; there is no hidden disk purge.

## Upstream contracts

- [Lume 0.5.3 CLI](https://cua.ai/docs/reference/lume/cli-reference)
- [Offline setup sequence](https://cua.ai/docs/concepts/how-lume-unattended-setup-works)
- [Pinned setup implementation](https://github.com/trycua/cua/blob/lume-v0.5.3/libs/lume/src/Unattended/MacOSOfflineSetupPatcher.swift)
- [Pinned SIP implementation](https://github.com/trycua/cua/blob/lume-v0.5.3/libs/lume/src/Commands/Sip.swift)

The live acceptance record is in `VERIFICATION.md`. Unit tests alone do not
establish that a particular macOS build's Setup Assistant or Recovery UI works.

The image journal uses schema version 2. Opening a v1 database preserves its
leases, requests and events and updates the version guard. Older core-only
binaries reject this database instead of expiring permanent image records.

## Reproducible live acceptance

`TestLiveImageRebuild` exercises the running v2 daemon through its HTTP API. It
**permanently deletes the exactly named configured golden image**, replaces it,
creates two disposable clones, checks third-VM refusal/idempotency, and deletes
only those test leases. Obtain authorization first. The daemon must be running
on port 7780 with a configured image profile and no active VM/lease operations.
The test never invokes image recovery; any failed stage requires inspection.
A verified cached IPSW is reused and fully hashed, so this does not by itself
prove a fresh network download. The initial live download is recorded separately.

```sh
VIRFIELD_LIVE_IMAGE_REBUILD=I_APPROVE_REBUILD_AND_TEMPORARY_VM_DELETION \
VIRFIELD_LIVE_TEMPLATE_ID=macos27 \
VIRFIELD_LIVE_TEMPLATE=macos-27-golden \
VIRFIELD_LIVE_TOKEN_FILE=/absolute/state/token \
go test ./internal/client -run '^TestLiveImageRebuild$' -count=1 -v -timeout=118m
```

Expect approximately 8–12 minutes with verified local media on the tested host;
network download time depends on throughput. Long deadlines bound installation,
not a reason to repeat a dispatched operation. The test retains the daemon's
journal and prints image/job IDs for exact inspection on failure.
