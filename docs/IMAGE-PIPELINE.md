# Image pipeline

Current image construction adopts digest-pinned Cirrus Tart images or secures a
Linux node's operator-installed bootstrap qcow2 golden. It does not install macOS
from IPSW in the live deployment. Old Lume/Recovery/OCR instructions are preserved
only in [history](history/IMAGE-PIPELINE-before-2026-10-14.md).

## Choose macOS and Xcode versions

`image_catalog` reports public macOS/Xcode metadata and configured image inventory.
`inventory.verified` refers to provider observation; `configured_images[].ready`
requires a present VM and matching durable `image_ready` fingerprint. Catalog
availability does not establish buildability, architecture or live acceptance.

`image_create` resolves only operator-approved Tart bases from `runtime.tart.bases`
plus configured Tart templates. `tart_bases` exposes their exact OS/build, digest
and SIP. Version/build/codename selects a matching base; default security requires
SIP enabled, while `sip-disabled`/`automation` require a disabled-SIP base. An
unsupported OS or policy is refused before VM creation. Xcode is optional and
pinned by exact stable version, build and SHA-1 from validated public metadata.
The configured default clone location is also the default image location; select
an explicit location when there is no unique default.

Configured bases and installed inventory remain available if public metadata is
offline (`metadata_error`); Xcode selection then requires metadata recovery.
Node profiles remain operator-configured and are not derived from Apple Silicon
restore metadata.

## Profile and dependencies

A Tart profile declares `image.tart.upstream` as an exact GHCR SHA-256 reference,
`macos`, `build`, the expected SIP state and `base-v1` without Xcode, `security-v1` for automation without Xcode,
`developer-v1` with exact Xcode, or `uitest-27-v1` for build `26A428`. The configured guest is `admin`,
with `/Users/admin` home. Templates name an allowed Tart container as `location`.

Packer and Tart are operator-owned executables. The embedded recipe pins the
Cirrus Tart plugin to 1.21.0; explicit plugin/runtime binary digest verification
is not yet implemented. Developer profiles download/resume and checksum-verify a XIP into
`runtime.tart.image.xcode_archives`. Existing valid archives are reused; corrupt
ones are quarantined. A failed Apple-authorized download in `resolve` supports
the native sign-in flow and resumes that exact job before any VM build. Install
the browser alongside virfieldd to enable this flow. UI-test profiles adopt
upstream Xcode. macOS 27 recipes keep the upstream disk and explicitly set `recovery_partition = "keep"`. Omitting it selects Packer's default deletion, which opens an ASIF file and can retain a lock that prevents Virtualization.framework startup. They do not resize or relocate Recovery. All Tart recipes use `1920x1080px` to avoid host Retina scaling; worker clones repeat this before boot and disable display refitting. The VM's Aqua agent selects and verifies the actual CoreGraphics 1920×1080 logical desktop on every login/preparation.

Older base/developer recipes grow the raw disk to 120 GB and require at least
40 GiB free on the writable `/System/Volumes/Data` filesystem. Capacity is checked
for up to 30 seconds after one resize; a sealed root snapshot's statistics alone
are not used for installation admission. Real shortage or invalid statistics fail
the build rather than accepting an undersized guest.
Image builds and portable exports reserve CPU/RAM/disk before dispatch and share
the worker quota. A new macOS 27 base requires an operator `disk_bytes` bound in
`runtime.tart.bases` (pinned profile: `build_disk_bytes`) or an observed golden of
that exact upstream. Unknown disk size is rejected before building; there is no
client-controlled quota bypass.

Tart stages are `resolve → build → qualify → stop`. Resolve downloads the pinned
upstream into the selected container before any Packer VM build, with automatic
cache pruning disabled. A failed source download can retry resolve without replaying
a VM mutation. Standard-account qualification retains `admin/admin` and installs
private SSH keys; legacy golden data is preserved under the policy below. Qualification reboots and verifies exact
OS/build, SIP, desktop, disk state, account authentication and, for Xcode profiles, signature/version/first launch/Swift
and DeviceSupport. Base profiles do not require Xcode or host DeviceSupport.
UI-test recipes additionally verify their automation tools and permission probes.
Evidence records upstream and recipe digests in private `verification.json`.

Node profiles declare `image.node.golden`, exact macOS/build and SIP. They do not
accept Xcode/provisioning fields today. `build → qualify → stop → commit` secures
an overlay, checks it after reboot and commits a new read-only qcow2 golden.
Its bootstrap OS installation is an operator task. The node uses a digest-pinned
Podman image and stores goldens outside the source checkout.

## Guest security policy

SIP is an expected upstream policy for Tart, not an automatic Recovery mutation.
An upstream with a different SIP state must fail. Tart accepts unlocked APFS
encryption at rest when FileVault is off because its clone keeps machine identity.
This is different from the retired Lume portable-disk contract.

The operator requires **`admin/admin`**, without automatic random account-password
rotation. Standard-account candidates (`0f8bc20` and later) implement this. Previously secured images retain their recorded credentials.
Only a new journaled disposable clone converts its known inherited account to
the standard password, verifies it and migrates the keychain while preserving
items. Already-standard accounts are authenticated without password change.
In-place securing of a legacy golden is refused before mutation; build a new
image instead. Unknown keychain credentials fail readiness. Never reset their
keychain or silently overwrite a golden. The policy still requires
per-image/lease management keys, caller-key isolation, fresh VM host keys and
pinned key-only SSH; inherited image/cross-lease keys must be rejected. Physical
host leases add only one caller key to the configured shared account.

The following keychain mechanics are retained with the standard account policy;
profile-specific live acceptance remains required.

VM qualification and lease preparation install an additional private keychain
named for the image/lease and an Aqua login LaunchAgent. They preserve/rekey the
inherited login keychain to the configured standard VM password and keep login as the
default and first search entry across boots. This lets loginwindow unlock it
before Spotlight and other early OS clients, which can otherwise queue password
dialogs for a custom default before the owned agent starts. The agent verifies
native access with the current password and unlocks before applying the
no-sleep/no-timeout-lock settings, and repeats at each GUI login. Passwords travel
on encrypted SSH stdin and live only in owner-only guest files; inherited keychain
files are preserved. A fresh native `WhenUnlocked` generic-password add/read/data
comparison/delete probe must succeed in the current GUI audit session and boot.
A successful `security unlock-keychain` or an unlocked status flag alone is not
proof that an application can store its encryption key.

SSH has a separate Security audit session. A fixed VM-only `sshd` ForceCommand
bridge unlocks that same private keychain immediately before each original client
command. It preserves command arguments/stdin, interactive shells and SFTP/SCP.
Readiness independently requires native key access from an SSH command session.
Full GUI readiness additionally requires a logged-in, unlocked console and an
available window snapshot without Keyboard Setup Assistant or SecurityAgent.
General/per-host screensaver idle is disabled; autologin preparation clears and
verifies the persisted lock flag. Existing login items are never deleted.
Failure is `guest_keychain_failed`; the image/lease does not become ready. This
policy applies to VMs, including node VM qualification, and does not alter shared
physical-host keychains. Existing ready images are historical evidence: new
qualification or new lease preparation is needed to obtain these gates.
The [Broker macos27 reacceptance record](VERIFICATION.md#broker-macos27-reacceptance)
tracks the exact failed versions, confirmed cleanup and newer deployment that
still needs its own production-path evidence.

`security: automation` applies the version-aware Gatekeeper, AMFI and TCC
policy inside the guest, then verifies its signed identities, grants, canonical
SIP, Gatekeeper and active boot arguments after reboot. `uitest-27-v1` additionally
checks its tools and UI automation permissions. New generic automation recipes
require live acceptance independently of the earlier UI-test acceptance.

## Registry

`registry_resolve` validates a Tart OCI manifest: digest, bounded descriptors,
canonical config → disk chunks → NVRAM order, chunk size/digest metadata and ASIF
file-group metadata. Lume/container/index formats are rejected. Tart handles blob
transport and reconstruction. Sources, organizations and credentials are
operator-configured; callers cannot choose an arbitrary registry or token.

`image_pull` pins the resolved manifest as the Tart upstream, retains provenance,
and builds/qualifies the expected OS/build/policy with the same pipeline.
`image_publish` requires an idle pool, verified stopped source, push-enabled
source and absent tag. It freshly rebuilds from the upstream recipe under a new
`vf-export-*` identity, verifies the public `admin`/`admin` account without adding
private image credentials, sanitizes, stops, pushes once and verifies the manifest
digest. It never uploads the private managed golden. Success waits for confirmed
removal of the temporary VM and private host files. Interrupted uploads stay
quarantined and are not blindly retried. There is no atomic GHCR create-if-absent
operation; reserve publication tags operationally to exclude concurrent writers.
Node goldens do not support Tart publication.

The complete fresh public-base factory/private push/pull-back/clone workload and
cleanup roundtrip passed on `2f23ae6` on 2026-10-15, preserving seven baseline
images. Retain that receipt and repeat for relevant export/import pipeline
changes; public manifest resolution alone is not that acceptance. See Verification
for digest and scope. The explicit local managed-golden transfer below avoids
registry publication; acquire itself still rejects cross-container clones.

## Recovery

Any ambiguous dispatched mutation stays reserved and requires exact-target
inspection. After confirming no process is in flight, use operator
`image-recover IMAGE_RECORD_ID EXACT_VM_NAME retry|delete CONFIRM-NO-OPERATION-IN-FLIGHT`
with a new stable recovery key. Tart `resolve` and `qualify` may be retried after
inspection; an interrupted Packer `build` cannot be blindly replayed. Node
`qualify` may be retried; `commit` is not replayable. Node deletion retains its provider and guest identity, protects backing goldens
with overlays and confirms Podman container absence before deleting files. No runtime-directory or SQLite bypass is a recovery.

`image-delete TEMPLATE_ID EXACT_VM_NAME` deletes a stopped unused image only after
explicit authorization. Images in use are protected. Rebuilds never overwrite.

## Acceptance

Only [Verification](VERIFICATION.md) owns accepted combinations and release gates.
An `image_ready` build is not a clone/workload acceptance run. Require a disposable
clone, pinned SSH, exact guest checks, artifact export and confirmed release.

## Verified local golden transfer

`image_transfer` / `POST /api/v1/images/transfer` accepts
`{"template":"SOURCE_TEMPLATE","id":"NEW_ID_AND_NAME","location":"DESTINATION_CONTAINER"}`
with a stable `Idempotency-Key`. CLI:
`virfield -token-file /PRIVATE/TOKEN -key STABLE_KEY image-transfer SOURCE_TEMPLATE NEW_ID DESTINATION_CONTAINER`.
The source implementation requires an idle pool, a stopped verified managed
standalone Tart image, another configured local storage and the full potential
disk size plus existing growth reservations and safety reserve. It does not
upload a managed golden or delete its source. Other providers, disk stacks,
suspended VMs, unknown resources and existing destination IDs/names are refused.

The journal pins source identity, profile and destination before dispatch. The
copy holds Tart's runtime config lock, preserves UUID/MAC/NVRAM, copies private
SSH identity and qualification evidence, and verifies source/copied file hashes
and storage identities. After all writes, the corrected copier enumerates source
holes with `SEEK_HOLE`/`SEEK_DATA` and deallocates the corresponding whole
destination filesystem blocks before full source/destination hash verification.
This is required on APFS: skipped zero buffers alone can allocate small sparse
gaps, and a 1 MiB buffer may span many holes. Unsupported sparse observation or
deallocation fails the operation and leaves the destination unregistered.
Registration is atomic only after a fresh stopped
runtime observation matches OS/resources. Source qualification remains dated
source evidence; an actual destination worker/native/reboot/workload acceptance
is a separate gate. The first installed implementation (`3d667d2`) is not live
accepted because a 44.8 GB allocated source became a 106.4 GB copy. That owned
test copy was deleted through its durable image-delete job after preserving the
receipt; all eight previous images and the source remain intact. The sparse
correction has a real red/green APFS regression (512 KiB allocated data in a
128 MiB logical file). Installed `a45655f` passed actual Monterey/Xcode transfer:
120 GB logical, 44.79 GB allocated destination, all source/destination hashes and
private identity equal. Two fresh complete native GUI/new SSH/reboot/Xcode/Swift/
workspace/artifact/RFB/release cycles passed; source and eight baseline manifests
remain unchanged. The accepted ninth image is retained. See Verification for the
exact durable job, image and receipts.

Interrupted/failed copies stay quarantined and reserve the source. Reusing the
same request key returns the same operation; it never repeats the copy. Inspect
the exact source, destination and job, and confirm no process remains in flight
before `image-recover IMAGE_RECORD_ID EXACT_VM_NAME delete CONFIRM-NO-OPERATION-IN-FLIGHT`.
Cleanup requires the matching destination ownership marker, including when a
partial disk has no runtime config. A collision or missing marker is not adopted
or deleted. Choose a new ID after confirmed cleanup; retry cannot skip copying
or promote a partial destination. SQLite schema 8 prevents an older executor
from treating a queued transfer as a normal worker clone. The pre-migration
schema-7 snapshot is private and integrity checked; old-binary rollback requires
that snapshot, not opening a schema-8 journal.
