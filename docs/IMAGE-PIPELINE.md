# Image pipeline

Build golden images from approved digest-pinned Tart bases or a Linux node's
operator-installed qcow2 bootstrap. Keep runtime disks and private build journals
outside the checkout. Build a new image instead of overwriting a golden.

## Choose versions and profiles

`image_catalog` lists public macOS/Xcode metadata and configured inventory.
`inventory.verified` describes provider observation; a ready configured image also
needs a matching durable `image_ready` fingerprint. Check your workload separately
on a disposable clone.

`image_create` selects from `runtime.tart.bases` and configured Tart templates.
Pin exact OS/build, upstream SHA-256 and SIP policy. Default security needs SIP
enabled; `sip-disabled` and `automation` need a disabled-SIP base. Unsupported
combinations fail before VM creation. Optional Xcode uses an exact stable version,
build and metadata checksum. Public metadata failure leaves configured inventory
visible with `metadata_error`, but Xcode selection needs metadata recovery.

A Tart profile has `image.tart.upstream`, `macos`, `build` and expected SIP state.
Use `base-v1` without Xcode, `security-v1` for automation without Xcode,
`developer-v1` for exact Xcode, or `uitest-27-v1` for build `26A428`. The guest is
`admin` with `/Users/admin` home; `location` names an allowed Tart container.
Node profiles declare `image.node.golden`, exact OS/build and SIP; they do not
accept Xcode/provisioning fields.

## Build and qualify

Tart stages are `resolve → build → qualify → stop`; node stages are
`build → qualify → stop → commit`. Submit a stable request key and poll the job.
Require job `succeeded` and image record `image_ready` before acquiring.

Packer/Tart binaries are operator-owned. The recipe pins the Tart plugin to
1.21.0; the operator must verify installed executable provenance. Resolve pulls
the pinned source with cache pruning disabled. Developer recipes verify and reuse
XIPs in `runtime.tart.image.xcode_archives`; corrupt archives are quarantined.

macOS 27 recipes retain the Recovery partition and upstream disk. Other
base/developer recipes grow the disk to 120 GB and require 40 GiB free on writable
Data, allowing up to 30 seconds for filesystem statistics after resize. Image
builds and exports reserve CPU/RAM/full disk growth before dispatch. macOS 27
bases need a configured `disk_bytes` bound (`build_disk_bytes` in a pinned profile)
or an observed golden of that upstream. Unknown size blocks admission.

Qualification reboots and checks OS/build, SIP, disk state, account authentication,
unlocked desktop and native GUI/SSH key storage. Developer profiles also check
Xcode signature/version/first launch, Swift and DeviceSupport; UI-test recipes
check automation tools/permissions. Display preparation requires a real
1920×1080 desktop. Private `verification.json` binds upstream and recipe digests.
Node qualification commits a new read-only qcow2 golden plus OpenCore config.

## Guest account and security

VMs use `admin/admin`, separate management and caller keys, fresh host keys and
pinned key-only SSH. A new clone can migrate a known inherited account/keychain
without discarding items. Unknown credentials fail readiness. Existing goldens
are preserved; in-place conversion from an older account policy is refused.
Physical leases add one caller key to the shared account and leave its keychain
unchanged.

SIP must match the approved upstream; the pipeline does not guess Recovery key
presses. Tart accepts unlocked APFS encryption at rest with FileVault off.
`security: automation` applies and verifies the recipe's Gatekeeper, AMFI, TCC and
boot-argument policy. See [keychain diagnosis](OPERATIONS.md#vm-keychain-readiness)
for native GUI/SSH checks and private failure logs.

## Apple sign-in

If a Tart Xcode resolve job fails because the archive needs Apple authorization,
request `image_apple_auth` for that exact job. Open its short-lived local browser
link and complete sign-in. The browser authorizes the daemon to resume that
job’s download/resolve stage; poll the same job ID. The native
browser sends scoped authorization to the loopback daemon. Never put Apple IDs,
passwords or cookies in tool arguments. A catalog entry does not grant download
access. The browser is unnecessary for valid cached/public artifacts.

## Registry import and publication

Configure allowlisted GHCR namespaces and private credential files.
`registry_sources` reports permissions without credentials. `registry_resolve`
checks the Tart manifest digest, ordered config/disk/NVRAM descriptors and chunk
metadata. Unsupported formats fail before import.

`image_pull` pins that manifest and qualifies the expected OS/build/policy through
the same pipeline. `image_publish` needs an idle pool, verified stopped source,
push-enabled namespace and unused tag. It builds a fresh portable `vf-export-*`
VM from the pinned recipe, verifies public bootstrap credentials, sanitizes,
stops, pushes once and checks the manifest digest. It never uploads the private
managed golden. Success includes confirmed temporary VM/file cleanup.

Reserve tags operationally: GHCR has no atomic create-if-absent operation.
Interrupted uploads remain quarantined for inspection. Node goldens cannot use
Tart publication.

## Transfer a local golden

`image_transfer` copies a verified standalone stopped Tart image to another
configured local container under a new ID/name. The pool must be idle and the
full potential disk growth plus reservations/reserve must fit.

```sh
virfield -token-file "$VIRFIELD_HOME/token" -key transfer-001 image-transfer SOURCE_TEMPLATE NEW_ID DESTINATION_CONTAINER
virfield -token-file "$VIRFIELD_HOME/token" job JOB_ID
```

The copy preserves UUID/MAC/NVRAM, private identity, qualification evidence and
sparse allocation, then verifies file hashes and storage/runtime identity before
atomic registration. The source remains intact. Unsupported sparse observation
or deallocation fails safely. Test a worker on the destination for your workload.

An interrupted copy remains quarantined and reserves its source. Same-key replay
returns the original operation. Inspect before cleanup; only the owned destination
with its matching ownership marker can be removed. A new attempt needs a new ID
after confirmed cleanup. Partial copies cannot be promoted or adopted.

## Optional legacy iOS toolchain

`image_create.toolchain: "legacy-ios-v1"` requires Tart macOS 12.7.6 and
Xcode 13.4.1/13F100 with `developer-v1`. Ordinary profiles omit it; existing
goldens are not provisioned in place.

```sh
virfield -token-file "$VIRFIELD_HOME/token" -key legacy-image-001 -location CONTAINER_ID -toolchain legacy-ios-v1 image-create NEW_ID 12.7.6 13.4.1
```

The [manifest](../internal/legacyios/assets/manifest.json) pins Ruby, OpenSSL,
LibYAML, Bundler and every source gem/archive by checksum. The root-owned native
toolchain lives under `/opt/virfield/toolchains/legacy-ios-v1`. Qualification
checks native Ruby/FFI/Ethon loading and an Objective-C armv7 compile/link with a
local CocoaPods dependency before/after reboot. It does not run a 32-bit device.
Source the profile environment in a subshell for caller builds:

```sh
( . /opt/virfield/toolchains/legacy-ios-v1/env.sh; bundle exec pod install --deployment )
```

Keep project lockfiles frozen. Partial installations are preserved for diagnosis;
setup does not overwrite them to pass. Private installer/verification logs retain
bounded failure output. System Ruby and global shell startup files stay unchanged.

## Recovery

Unknown dispatched mutations retain reservations. Confirm that no operation is in
flight, inspect the exact image/VM and preserve diagnostics before recovery:

```sh
virfield -token-file "$VIRFIELD_HOME/token" -key inspected-image-001 image-recover IMAGE_RECORD_ID EXACT_VM_NAME retry CONFIRM-NO-OPERATION-IN-FLIGHT
```

Tart `resolve`/`qualify` and node `qualify` can retry after inspection. Interrupted
Packer builds and node commits cannot be blindly replayed. For authorized cleanup,
use `delete` instead of `retry`. `image-delete TEMPLATE_ID EXACT_VM_NAME` deletes
only a stopped unused image; backing overlays and active operations block it.
Never bypass these checks by editing SQLite or deleting runtime directories.
