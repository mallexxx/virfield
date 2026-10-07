# Image Manager

Run command examples from the repository root. `VIRFIELD_HOME` refers to the
private deployment directory defined in [Operations](OPERATIONS.md#paths-and-configuration).

Image preparation is a durable job in `virfieldd`. CLI, UI and MCP submit the same
API request. No client invokes Lume directly. Broker/Runner execution and the
installation of provider binaries remain separate. The optional fixed
`uitest-27-v1` recipe automatically installs Xcode/tools and configures guest-only
SIP/Gatekeeper/AMFI/TCC and sudo. The operator selects and authorizes it once for the image profile; each subsequent `image-build` executes it automatically.

## Choose macOS and Xcode versions

`image_catalog` (MCP) or `image-catalog` (CLI) lists current restore releases and
stable Xcode versions. `image_create` accepts `id`, `macos`, optional `xcode`,
optional named `location` (default `home`) and `idempotency_key`:

```json
{"id":"sequoia-xcode162","macos":"15.2","xcode":"16.2","idempotency_key":"sequoia-xcode162-001"}
```

```sh
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" image-catalog
"$VIRFIELD_HOME/bin/virfield" -token-file "$VIRFIELD_HOME/token" -key sequoia-xcode162-001 image-create sequoia-xcode162 15.2 16.2
```

The HTTP equivalents are `GET /api/v1/images/catalog` and `POST /api/v1/images`
with the request fields above except the key, which uses `Idempotency-Key`.
Use an exact macOS version, build, or codename (`big-sur`, `monterey`, `ventura`,
`sonoma`, `sequoia`, `tahoe`). A codename resolves to the highest available point
release. Xcode selection is an exact stable version. The catalog is metadata from
[IPSW.me](https://api.ipsw.me/v4/device/VirtualMac2,1?type=ipsw) and
[Xcode Releases](https://xcodereleases.com/data.json); binaries are fetched only
from their validated Apple HTTPS endpoints. Metadata is cached for ten minutes.
An unavailable or malformed catalog fails explicitly; there is no guessed URL.
Each Xcode entry also reports `local_state`: `available` (catalog metadata only),
`downloaded` (completed private cache), or `installed` (at least one verified
`image_ready` golden). `installed_images` lists those template IDs. This is a
snapshot, not a promise that Apple will authorize a new download; the download
stage rechecks archive integrity before installation.

Acceptance atomically persists the resolved manifest, template and build job in
SQLite. Retrying an accepted request returns its original selection even if the
catalog changes or is offline. The template survives daemon restart and appears
in status; no config-file edit is needed. IDs and VM names cannot overwrite an
existing template. Storage must be in the operator's `storage_paths`.

Catalog images keep SIP enabled unless `security` explicitly selects another policy. Without Xcode they produce a base desktop image;
with Xcode they use `developer-v1`: install the selected Xcode, accept its license,
complete first launch, verify its exact version/build, Apple signature and Swift
compilation again after reboot. Guest developer provisioning enables passwordless
sudo for the disposable worker account. It does not install the separate UI-test
tool suite. The full macOS 27 UI-test tool suite below remains explicitly
version-specific; guest security is independent of that tool suite. Other versions are not globally rejected, but Apple's
Virtualization framework must support the restore image on the actual host.
Unknown Assistant screens fail rather than publishing an incomplete desktop.

Final verification also mounts the golden VM's dyld cache read-only and
atomically extracts it into Xcode's host-side `macOS DeviceSupport/<version>
(<build>)/Symbols` directory. This happens before the golden becomes ready and
also runs after a registry pull on a new host. A complete matching directory is
reused; a partial directory is preserved with an `.invalid-*` suffix.

### Golden disk policy

Default portable golden boot volumes must be unencrypted. Native Setup Assistant leaves FileVault
unchecked, re-observes the control after changing it, and verifies the resulting
System and Data volumes over SSH. The assistant stage and final post-reboot
verification require `diskutil info -plist` to report `FileVault=false`,
`Encryption=false` and `Locked=false` on both volumes. Missing or unknown values
fail closed. The final verification manifest includes both volume states.

`FileVault is Off` alone is insufficient: the tested Monterey 12.6 guest reports
`Encrypted at rest` even after completing Setup Assistant without FileVault.
[Apple documents this distinction](https://support.apple.com/en-gb/guide/security/sec4c6dc1b6e/1/web/1).
Under the default portable policy, such a guest returns `disk_encrypted` and
cannot be published. The Monterey 12.6 / Xcode 13.4.1 image reached
`image_ready` on 2026-10-03 under the explicit identity-preserving legacy
policy below. One disposable clone passed boot, workload, artifact export and
cleanup on the same day. Existing
images need re-verification; upgrading the binaries does not retroactively
certify them.

Legacy option for Monterey and other identity-bound guests:
`identity-preserving` templates may be configured with `legacy_uuid`. Virfield
requires this field in the operator's `config.json` template before `image-build`;
the catalog `image-create` command does not set it. A Monterey image created
without it fails the APFS encryption check and is quarantined. Virfield
persists that UUID on image-build and worker leases, allows APFS
encryption-at-rest for that legacy image only, and refuses a second active
worker with the same UUID until cleanup confirms release. FileVault and locked
volumes still fail closed. The golden must stay stopped. Cleanup must delete the
copied bundle. Registry publication and portable export remain disabled,
because the image is not a portable golden. Acceptance requires a live test
proving same-identity copy → boot → SSH → workload → artifact export → cleanup,
plus refusal of a second concurrent worker. The Monterey/Xcode image passed
that test on 2026-10-03. Lume normally regenerates `machineIdentifier` and
`macAddress` during clone; Virfield restores both values from the stopped source
before booting a legacy clone and checks that NVRAM was copied. Ordinary clones
keep their fresh identity. This is a separate legacy contract,
not a weakening of the default unencrypted clone policy.

### Guest security policy

The API and MCP `image_create` accept `security`; the CLI uses `-security` before
`image-create`. The selection is persisted in the immutable manifest and retry
fingerprint, independently of Xcode:

| Policy | Guest behavior |
| --- | --- |
| `default` or omitted | Keep SIP and system protections enabled |
| `sip-disabled` | Disable SIP through paired Recovery; verify canonical status after normal boot |
| `automation` | Also disable Gatekeeper, preserve existing boot arguments while adding the AMFI flag, and grant Terminal/SSH Accessibility, Screen Capture, Full Disk Access and AppleEvents to System Events |

Example: `virfield -security automation -key sequoia-ui-001 image-create sequoia-ui 15.2 16.2`.
The supported Recovery generations are macOS 11–15, 26 and 27; availability still
depends on Apple's restore catalog and the host's virtualization support. This is
an implementation range, **not a claim that every release has passed live acceptance**.
See [Verification](VERIFICATION.md) for tested combinations. Unknown UI or TCC
schemas stop the job and prevent publication.

Only SIP needs Recovery console input. Gatekeeper, boot arguments and TCC are
configured over authenticated SSH with fixed embedded commands. TCC grants use
installed code-signing requirements and schema discovery; no downloaded script
is executed. Verification checks the grants, canonical SIP status, Gatekeeper,
active AMFI boot argument and AppleEvents again after reboot. AMFI evidence is
configuration evidence; it does not assert that every future OS honors that flag.
This policy does not install third-party UI tools or grant permissions to every
application. Xcode installation remains optional and separately verified.

Upstream references: [Lume SIP implementation](https://github.com/trycua/cua/blob/main/libs/lume/src/Commands/Sip.swift),
[guest TCC seeding](https://github.com/trycua/cua/blob/main/libs/cua-driver/tests/runners/macos-lume/seed-tcc-guest.sh),
and [paired Recovery constraints](https://cua.ai/docs/concepts/how-sip-works-in-lume-vms).
Virfield adapts these mechanisms to its pinned SSH credentials, one-slot image
reservations and observed Recovery transitions. It never changes host protections.

### Apple Developer downloads

Some Xcode versions require an Apple Developer session. If a job reports
`apple_auth_required`, an agent calls `image_apple_auth({"id":"JOB_ID"})` and
sends the returned 15-minute `url` privately to the user. The loopback page
only launches Virfield's native Apple browser. Its private WebKit window opens
`developer.apple.com` for the exact Xcode release. The user enters the Apple
Account and any 2FA challenge on Apple's page. When Apple begins the XIP
download, the browser sends the scoped Apple session to the loopback daemon;
the daemon downloads and verifies Xcode and automatically retries the exact
durable job. No manual XIP download is part of this path. The browser does not
send the password or verification code to Virfield, MCP or Mattermost. A daemon
restart invalidates an unfinished link; request a new one for the same job.
This uses an Apple Developer website session, not a Sign in with Apple OAuth
grant. The native browser must be installed and registered on the Virfield Mac.

The operator may alternatively configure either or both preexisting private
sources in `image_tools`:

```json
{
  "xcode_archives": "/absolute/private/imports",
  "apple_cookies": "/absolute/private/apple-download-cookies.txt"
}
```

`xcode_archives` is optional: place an official `Xcode_VERSION.xip` there, for
example `Xcode_13.4.1.xip` downloaded from
[Apple Developer Downloads](https://developer.apple.com/download/all/).
`apple_cookies` is an optional Netscape-format file, mode 0600, containing the
operator's authorized Apple download session. Only unexpired matching Apple
cookies are sent, only to `download.developer.apple.com`; HTTPS redirects to
Apple hosts are followed without forwarding credentials. Keep the file outside Git. The repository secret scan also detects Apple download-session
cookies in Netscape, header and JSON formats. A matching local Apple-signed bundle from `image_tools.xcode` is reused first
(the version/build must match exactly); otherwise the archive is imported
before attempting an authenticated download. Neither path nor cookies can be
supplied through MCP/API.

Expired/missing authorization returns `apple_auth_required`. Download/import verifies the catalog SHA-1
(the metadata source's available checksum); macOS `xip` additionally verifies
Apple's archive signature, and `codesign` checks an intact Apple-signed app on the host and in the guest
before executing Xcode.
Checksums alone are not the Xcode trust boundary. Version/build metadata is read
without executing the older Xcode on the host. Expanded apps and archives are
cached privately by digest. Host Xcode selection is unchanged.

After manually placing an archive or renewing the operator cookie file, inspect
the failed job and use the documented download-stage `image-recover ... retry`
command below. Browser sign-in resumes it automatically. No VM
is created until the required downloads and Xcode expansion pass.

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
  "lume": "/absolute/private/tools/lume/lume",
  "python": "/absolute/private/tools/vnc/bin/python",
  "tesseract": "/opt/homebrew/bin/tesseract",
  "vnc_bin": "/absolute/private/tools/vnc/bin"
}
```

The Lume adapter is pinned to patched Lume 0.5.3. Base/developer images use
the selected restore build; only the full `uitest-27-v1` tool suite is pinned to
26A428. Install `vncdotool==1.3.0` into an isolated
Python environment and Tesseract before starting a build. The tested Python
3.14.4 package set is pinned in `deploy/image-tools-requirements.txt`; Tesseract
5.5.2 was used for live acceptance. The daemon never
installs host dependencies itself. Build the patched dependency and create the
isolated VNC environment before configuring their absolute paths:

```sh
mkdir -p "$VIRFIELD_HOME/tools"
bash deploy/build-lume.sh "$VIRFIELD_HOME/tools/lume"
python3 -m venv "$VIRFIELD_HOME/tools/vnc"
"$VIRFIELD_HOME/tools/vnc/bin/python" -m pip install -r deploy/image-tools-requirements.txt
```

Use the selected Python interpreter for `python3`; install Tesseract separately
and set its absolute executable path. The Lume output directory must be new.
For the full profile, also configure the absolute local Xcode source as described
in [Optional UI-test tool profile](#optional-ui-test-tool-profile).

`assistant.py` is an embedded guest
VNC finisher whose recognized screens are tested on macOS 27; it cannot
manage VM lifecycle. Lume's native `setup` handles offline patching. In pinned
0.5.3 its `tahoe` preset contains no UI boot commands or version guard: it selects
the offline account/SSH setup, while the IPSW manifest selects the macOS version. Recovery
uses one owned `lume run --recovery-mode true` child plus `recovery.py`: the
native 0.5.3 SIP navigation was observed to open Time Machine on this build.
The patched Recovery child reads its VNC password from a private file rather
than process arguments. It asks the kernel for port 0 and accepts only the
newly written `sessions.json` endpoint with the exact password for this run;
there is no listen-then-close port reservation.
The driver handles optional language and volume-owner screens, and verifies
Options, Utilities, Terminal and each
authentication prompt before input. It accepts only an explicit successful SIP
policy response, requests guest shutdown and waits for fresh stopped inventory.
Normal-boot `csrutil status` is still the mandatory final policy check. Unknown
UI screens fail with private diagnostic screenshots. The finisher waits
for boot frames, reconnects for full VNC framebuffers, recognizes the final
button separately from the wallpaper and delegates the final desktop
postcondition to SSH. Each attempt retains its own screenshot directory.

The supported recipe fixes resources at 4 CPUs, 8 GiB RAM and NAT. New images
with a selected catalog Xcode use a 120 GB sparse disk so guest XIP expansion
has room; other new images use 80 GB. This is a virtual maximum,
not an immediate host allocation. The existing Monterey/Xcode VM remains 80 GB
and used about 49 GiB on the host after installation. Its XIP was 10 GB, its
cached `Xcode.app` used 17 GB on the host, and the uncompressed file stream was
about 32 GiB. Guest `xip` refused expansion with about 42 GiB free on that VM.
The setting applies at VM creation and does not resize existing images.
Base image, setup, SIP, verify and worker boots use no host shared folders.
For operator-provided Xcode app bundles on macOS 13+ guests, developer
provisioning starts the image VM through native `lume run --shared-dir`, sharing
the source parent directly over VirtioFS and copying from `/Volumes/My Shared
Files` inside the guest with `ditto`. Before each retry Virfield detaches and
cleans any stale `virtiofs-transfer` directory left by older builds. Apple
Virtualization only automounts VirtioFS in macOS 13 or newer guests.

Catalog XIPs use a separate transport on every supported macOS version. The
host checksum-verifies the archive, streams it over pinned SSH, verifies its
checksum in the guest, and expands it with `xip` there (which verifies Apple's
signature). Host-side `xip` requires a login audit session that the launchd
daemon cannot reliably enter. The guest checks bundle version/build and
signature, then installs it as `/Applications/Xcode.app`. Transfer progress is
reported, and retry removes
stale partial archives and expansion directories. This archive path requires
the guest disk to hold the XIP and expanded app during installation. If `xip`
reports insufficient guest space, Virfield streams the already verified host
`Xcode.app` cache over the same pinned SSH connection, preserving file contents,
permissions and links while omitting extended attributes that Monterey's tar
cannot restore reliably. It checks version/build and Apple's deep signature
before replacing the guest app.
This keeps the signed XIP as the normal catalog transport while allowing
existing 80 GB Monterey images to install Xcode without resizing encrypted APFS disks.
Display is 1920×1440 for native macOS 11–13 and Sequoia setup (their
Assistant controls can be clipped at 1080 pixels), and 1920×1080 otherwise.
Changing the recipe requires a code change and live validation; this is not an
arbitrary command runner.

## Build and inspect

```sh
./bin/virfield -token-file /absolute/state/token -key build-macos27-001 image-build macos27
./bin/virfield -token-file /absolute/state/token job JOB_ID
```

A build requires one available VM slot and an absent target name. An unrelated
ready lease may use the other slot while the image builds.
It holds maintenance admission until ready or explicitly cleaned up. Image
records have no lease expiry. A successful stopped image consumes no VM slot.

The persisted stages are download → create → setup → assistant → SIP →
provision (when configured) → verify → stop → ready. Each external effect has a dispatched checkpoint and a separate
completion event. The manifest is copied into the accepted job, so changing
configuration during a restart cannot change an in-progress restore image.

The download is real HTTPS from Apple, with no redirects. IPSW transient network failures get at most three range-resume attempts. Xcode retains partial bytes for an explicit retry. A graceful
daemon shutdown pauses a download for automatic resume on restart. Partial downloads use
Range with validated Content-Range. Only exact size plus a full SHA-256 permits
atomic cache promotion. Disk-space checks run before download and installation.
A cache hit is also hashed before use. Progress is stored every five seconds;
API reads remain fast while the worker performs network or VM operations.

Golden shutdown is requested inside macOS over pinned SSH and must be observed
as stopped within two minutes. It never falls back to hypervisor power-off.
Lume 0.5.3's `stop` calls `VZVirtualMachine.stop`, which is unsuitable as the
normal persistence boundary for freshly written guest account/TCC state. Final
verification authenticates the image password again after a clean reboot, in
addition to key-only SSH. Disposable-VM deletion uses the separate cleanup path.

The service requires the local Lume 0.5.3 patches in
`deploy/lume-0.5.3-guest-shutdown.patch` and
`deploy/lume-0.5.3-registry-integrity.patch`, plus the version marker patch.
Upstream `runVM` leaves a successfully
completed guest in `SharedVM`, so `/lume/vms` and `/lume/host/status` keep reporting
it as running after a normal shutdown. The patch removes that entry when `run`
returns. It also replaces the successful unattended setup's forced power-off with
one guest shutdown request and waits for VM lifecycle cleanup, including when SSH
disconnects during shutdown. Do not work around stale inventory by force-stopping the guest or by
ignoring the capacity response. `bash deploy/build-lume.sh NEW_ABSOLUTE_OUTPUT_DIR`
verifies the source archive SHA-256, uses the upstream `Package.resolved`, applies
all four patches, runs the four focused Swift regression tests, and signs a
NAT-only binary with the virtualization entitlement.
The image pipeline accepts only `lume --version` equal to
`0.5.3-virfield6`; stock 0.5.3 is rejected before any download or VM mutation.
The registry patch propagates child upload errors and makes OCI push fail if
any disk part is missing instead of publishing an incomplete manifest.
Virfield's registry resolver also checks that annotated disk parts cover the
full declared size before import or publication success is accepted.
It does not install or restart services. Configure both the Lume service's
`-config` and reads `image_tools.lume` at start, so future Lume upgrades only
need a private config or stable tool-path update, not a root LaunchDaemon edit.
The same recipe runs
cache, setup-shutdown and registry-integrity regression tests before packaging.

Lume subprocesses have fixed argv, bounded private logs, context deadlines and
an owned process group. Cancellation never uses `pkill`, process-name searches,
or kills the Lume service. No raw process output or VNC password enters events.

Guest provisioning uses Go SSH, a pinned guest host key and a `VirtualMac`
hardware check before privileged operations. The documented `lume/lume`
bootstrap is used only for a newly installed or explicitly imported portable image.
Registry imports must already boot with SSH and the native `lume`/`lume`
bootstrap account. Their setup runs through SSH and preserves directory-service
records and volume ownership; offline setup is only for fresh IPSW installations. After Assistant completes,
the manager establishes generated image-specific credentials, disables SSH password
authentication, checks effective `sshd -T` policy and creates a VM-local workspace.
Automatic login is configured through the guest's root-only `kcpassword` and
loginwindow preference. The manager verifies the decoded password over pinned SSH
without returning it; `sysadminctl -autologin` exit status is insufficient on
headless Sequoia. This verification also applies to each worker clone.
This happens before Recovery; the Recovery driver receives the generated password
on private stdin. Final verification checks guest build, canonical SIP status,
credentials and Finder after another reboot. Credentials remain in private 0600 files under
the private state directory; they are never returned by status/job/events.
Each disposable clone receives a distinct password, management key and SSH host key before readiness. Caller keys are supplied per lease; image/cross-lease keys are tested for rejection. See the [SSH contract](OPERATIONS.md#leases-and-ssh).

## Native setup for macOS 11–13

These guests use their native Setup Assistant because their encrypted APFS Data
volume may be unlockable only by the running guest. The executor recognizes
visible screens and OCR text anchors, creates the fixed bootstrap account,
enables guest SSH, establishes automatic login, and shuts down cleanly. The
next journaled stage rotates the bootstrap password and hardens SSH. Unknown
screens or missing controls stop with private screenshots; they never publish
an unverified image. The subsequent SIP stage applies the selected security policy.

An inspected failed setup of macOS 11–13 can be resumed with `setup-online`:

```sh
./bin/virfield -token-file /absolute/state/token -key inspected-online-setup-001 \
  image-recover IMAGE_RECORD_ID EXACT_VM_NAME setup-online CONFIRM-NO-OPERATION-IN-FLIGHT
```

This distinct recovery action resumes the visible Assistant in the owned guest;
it never repeats restore or offline disk patching. The exact VM must be running
or stopped with no setup process in flight. Ordinary `retry` still refuses an
interrupted create/setup. The operator-only action is absent from MCP.

## Recovery and deletion

An interrupted VM mutation is quarantined and holds admission. A completed
checkpoint can continue after restart; an unconfirmed create/setup cannot be
replayed. Download bytes can safely resume, but a failed download can also be retried explicitly. VM mutation recovery requires an
operator to inspect the exact VM and confirm that no operation remains in flight.

```sh
./bin/virfield -token-file /absolute/state/token -key inspected-image-retry-001 \
  image-recover IMAGE_RECORD_ID EXACT_VM_NAME retry CONFIRM-NO-OPERATION-IN-FLIGHT
```

`retry` is limited to download, assistant, SIP, provision and verify at their persisted
failure stage. `reprovision` is an explicit operator action after failed UI-profile verification: it reruns the configured provisioning recipe and all reboot checks. Ordinary `retry` of verification never silently reruns provisioning. Existing Xcode is reused only when its full version matches and its deep code-signature verification passes. It cannot skip verification or change the VM identity. A failed
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
absence. Cache media are retained; journal history follows the 30-day policy
documented in Operations. There is no hidden disk purge.

## Upstream contracts

- [Lume 0.5.3 CLI](https://cua.ai/docs/reference/lume/cli-reference)
- [Apple VirtioFS directory sharing](https://developer.apple.com/documentation/virtualization/vzvirtiofilesystemdeviceconfiguration)
- [Offline setup sequence](https://cua.ai/docs/concepts/how-lume-unattended-setup-works)
- [Pinned setup implementation](https://github.com/trycua/cua/blob/lume-v0.5.3/libs/lume/src/Unattended/MacOSOfflineSetupPatcher.swift)
- [Pinned virtualization stop implementation](https://github.com/trycua/cua/blob/lume-v0.5.3/libs/lume/src/Virtualization/VMVirtualizationService.swift)
- [Pinned SIP implementation](https://github.com/trycua/cua/blob/lume-v0.5.3/libs/lume/src/Commands/Sip.swift)

The live acceptance record is in [Verification](VERIFICATION.md). Unit tests alone do not
establish that a particular macOS build's Setup Assistant or Recovery UI works.

The checkout journal uses schema version 7 for 30-day retention indexes; the
installed candidate still uses schema 6. Image records were introduced in
schema 2. Supported migrations preserve leases, requests and events, snapshot
the old database before changing it, and update the version guard. Older
binaries reject newer schemas. Check the deployment's
`release.json` before assuming that a later checkout build is installed.

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

Allow 15–25 minutes for the full tool profile on the acceptance host;
network download time depends on throughput. Long deadlines bound installation,
not a reason to repeat a dispatched operation. The test retains the daemon's
journal and prints image/job IDs for exact inspection on failure.

## Optional UI-test tool profile

Set the operator-owned image profile `provision` to `uitest-27-v1` and
`image_tools.xcode` to an absolute complete local `Xcode.app` or `Xcode-beta.app`, version 27 or newer. Compatibility is checked before downloading media or changing the guest. The source bundle is installed as `/Applications/Xcode.app` inside the guest; the host selection is untouched. The recipe requires
`disable_sip: true` and build `26A428`. It starts the VM with the verified
source directory shared through VirtioFS, copies it inside the guest with
`ditto`, completes Xcode first launch, installs the pinned Homebrew installer
and tools, and configures guest-only Gatekeeper, AMFI, TCC and passwordless sudo.
Homebrew packages are currently resolved from their taps at provisioning time;
this is a versioned recipe, not a fully reproducible package lock.

A new build adds `provision` between SIP and final verification. For an already
verified stopped base image, after the guest policy is authorized:

```sh
./bin/virfield -token-file /absolute/state/token -key provision-macos27-001 \
  image-provision macos27 macos-27-golden
```

This revokes publication immediately, reserves exclusive maintenance access and
requires reboot verification before promotion. It verifies Swift execution,
required binaries, passwordless sudo, Gatekeeper, AMFI boot arguments, Terminal
TCC entries, System Events and actual local Peekaboo required permissions. A
failure leaves the image unavailable; it never publishes based only on a shell
script exit code. Provisioning is a CLI/operator operation, absent from MCP.

The guest recipe is embedded and has VirtualMac/build guards. It does not import
host SSH keys, reset guest credentials to `lume`, mount host folders or accept
caller shell commands. Logs are private and bounded by the SSH transport. Publication requires all verification probes. See [Verification](VERIFICATION.md) for tested tool versions and results; upgrading Xcode or Homebrew tools requires another image acceptance.

The guest Gatekeeper setting uses Apple's Security `policydb` contract:
`SystemPolicy-prefs.plist` stores the string `enabled = no`, with readable 0644
permissions. The old Boolean `EnableAssessment` did not disable assessment.
See [Apple policydb.cpp](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_codesigning/lib/policydb.cpp)
and [policydb.h](https://github.com/apple-oss-distributions/Security/blob/main/OSX/libsecurity_codesigning/lib/policydb.h).
On macOS 27, the user's database lives in a protected container. The recipe starts the registered GUI user's tccd job, validates its UID, and resolves its one open TCC database through lsof. It validates the allowed path and existing schema before writing; it never creates an empty HOME database or imports another user's decisions. Failed grants stop provisioning.
