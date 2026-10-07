# Verification

This is the current acceptance record and test runbook. It distinguishes the
verified local host and image-tool deployment from the unaccepted full Balda
workflow. Results were collected on Apple Silicon macOS through 2026-10-07 (timestamps UTC),
using Go 1.26.8 and patched Lume 0.5.3. The full UI-test suite is verified on
macOS 27 build `26A428`; versioned image coverage is recorded separately below.

## VS Code remote LLDB (2026-10-07)

The DuckDuckGo SwiftPM worktree was built on the host with its existing
SwiftBuild tasks, ad-hoc signed, and incrementally copied to a macOS 27.0
Virfield lease. Host LLDB 2103.0.34.105 connected through an SSH tunnel to the
matching guest `debugserver`. With on-demand symbols, minimal remote module
loading, and system libraries extracted from the read-only NFS dyld cache, the
generated LLDB-DAP configuration connected in 0.14–0.15 seconds and reached
`DuckDuckGoBrowserMain` in a median 7.56 seconds. It reported 1,150 loaded
modules without transferring `libobjc.A.dylib` from process memory. Three
repeated runs were 7.51, 7.56, and 7.72 seconds.

## Pending release gates

The installed versioned-image candidate is **not production accepted**. Before
registering it in Codex and Claude Code, complete:

1. Run the identity-preserving clone/workload/cleanup check from the newly built
   120 GB Monterey/Xcode image. The earlier 80 GB image passed this test; the
   fresh image has not yet been cloned.
2. Complete the full Balda/Broker workflow using the accepted image, including
   pinned SSH workload and artifact delivery through the intended client path.
3. Install the accepted build, register `virfield` in both clients using
   [Operations](OPERATIONS.md#register-codex-and-claude-code), and validate the
   [agent workflow](AGENT-GUIDE.md#client-registration-and-acceptance) in each.
4. Produce a clean release archive with the native Apple browser, then verify
   its URL scheme and app signature after extraction. The release script now
   builds the browser from committed source and packs files with fixed archive
   metadata; the clean artifact still needs acceptance. The legacy
   XcodesLoginKit helper is no longer built or distributed.

The Sequoia 15.2 GHCR publish → pull-back → ordinary clone/workload and cleanup
gate passed; its exact digest and live evidence are recorded below. A clean
unattended Monterey/Xcode build passed on 2026-10-03. The earlier 80 GB image
passed the legacy clone test; the new 120 GB image and full Balda/Broker
workflow gates remain open.

The agent guide and operations/image/verification runbooks are embedded in MCP
as `virfield_help` and four read-only resources. Automated protocol tests verify
help without a daemon, resource/help equality, startup instructions, topic
allowlisting and documentation coverage for every exposed tool. This does not
claim that either client has passed live workflow acceptance. The release
remains pending until the client checks pass.

## Apple website sign-in candidate (2026-10-02)

The checkout now exposes 18 MCP tools, including `image_apple_auth`. A private
15-minute link opens a native WebKit window at `developer.apple.com`. The API
returns a direct `deep_link` and a one-click loopback redirect for chat clients
that do not linkify custom URL schemes. The optional fallback page has no
credential fields. The browser
passes only scoped download-session cookies to the daemon after Apple sign-in.
The daemon writes only scoped Apple download cookies to an owner-only file and
queues a retry of the exact failed download job. Unit tests cover bearer access,
cross-origin refusal, link rotation/expiry, cookie-file permissions, exact-job
resume and redirect cookie stripping. The native browser and Go binaries build
on the host. A real Apple sign-in through this window resumed the exact
Xcode 13.4.1 job on 2026-10-02; at 07:43 UTC it was downloading at 8%.
The later download/install and legacy clone passed on 2026-10-03.
The previous
local credential form was removed from the HTTP route after a live failure.

## Live image-job reporting (2026-10-02)

Broker rc.16 stores image-job watches in SQLite and polls Virfield for new
phases, errors and terminal results. It delivers a reference-only webhook to
the exact configured Balda Mattermost conversation with a stable idempotency
key; watch records are retained for 30 days. The route for job
`job-057b0898f3746df8b12fb8b5c4c34c1c` was registered and delivered an
actual `xcode_signature_failed` error reply to the original Mattermost thread,
confirmed by the operator. This verifies error reporting for that thread.
After correcting the host `codesign` timeout, the same job completed download
verification at 11:47:31 UTC and entered `create_dispatched` at 11:47:33 UTC.
Balda received the new-phase webhook in the same thread. Native Setup
Assistant completed, but the same job was then `needs_attention` with
`disk_encrypted`: `diskutil` reports `FileVault=false`, `Encryption=true`,
`Locked=false` on both Monterey boot volumes. At that checkpoint the image was not published and
its VM was stopped. Its ordinary clone has not passed acceptance.
Other conversations need their own configured notification route before a
watch can be registered. The image and identity-preserving clone later passed;
the default portable clone mode remains unsupported for Monterey.

## Headless guest audio and Monterey setup (2026-10-02)

The Monterey/Xcode job reached native Setup Assistant. Lume had no viewer open
but still attached `VZHostAudioOutputStreamSink`, so the guest's spoken language
prompt played through the host. Patched Lume `0.5.3-virfield6` now omits guest
audio devices when `display_mode=none`, which is the mode Virfield requests for
image setup. The accidentally opened VNC viewer was closed. The job's VM was
stopped before updating Lume and Virfield, then resumed through the existing
`setup-online` recovery; no replacement image was created. The root-owned
Lume launchd plist still names the old v4 directory because passwordless sudo
is unavailable; its binary was replaced with the verified v6 build, and the
image-tool configuration points to the separate v6 directory. Both now report
the v6 marker. Monterey's adjacent `Don't Skip`/`Skip` buttons and an OCR-missed
`Full name` label required verified crop fixes. OCR reads now get one bounded
retry after a transient Tesseract timeout. Native Setup Assistant completed
and SSH came up. At that checkpoint the default APFS disk-policy check failed
before Xcode installation; the later legacy-policy recovery completed the image.

## Audit response and partial live acceptance (2026-10-02 checkpoint)

The 2026-10-02 audit led to source and test changes for scoped principals and
lease ownership, token rotation, release convergence, backend-outage deadlines,
rejected starts, one-slot image reservations, verified download caches, a
distinct patched Lume version, Broker SSH reuse and image tools, and the local
Apple sign-in handoff. `make check`, `make build`, Broker race tests and the
selected Swift patch tests passed in this checkout. The installed Virfield and
Broker binaries and the system Lume daemon now have these changes.

A live Balda conversation on 2026-10-02 asked for a pre-existing Monterey
worker because `execution_profiles` listed only `os_version: 27.0` workers.
That field describes the execution golden, not the requested image build.
The configured `macos-xcode` worker is the Virfield connection for image tools.
Broker rc.11 now exposes five `virfield_image_*` MCP tools; Balda's installed
`execution-broker` plugin was upgraded from rc.11 to rc.15. The scoped Broker principal
uses a distinct token and `image:build` access through the container Host
allowlist. A live MCP call of `virfield_image_catalog` using `macos-xcode`
returned 59 macOS and 67 Xcode catalog entries. This establishes the image
catalog path, not a completed image build or a new Balda conversation. The
`os_version: 27.0` execution golden is not a restriction on image creation.
No direct guest SSH or macOS 27 substitute is needed for the request.

The scoped catalog now also returns a fresh, read-only `inventory` with actual
observed VM names/states and configured image recipes. `verified` marks a fresh
Lume observation; `present` and `ready` distinguish a recipe from a VM and a
finished golden. A live scoped Broker MCP call carried the inventory through:
six stopped VMs were observed, none was Monterey, and configured
`macos-monterey-golden` (12.6) reported `present=false`, `ready=false`. This
recipe cannot be treated as an existing VM for an Xcode install. Balda's
`execution-broker` plugin rc.15 tells new sessions to use this inventory, compare
the requested Xcode to the pinned profile before `image_build`, and create a
compatible image when none exists. Existing Balda conversations keep
their pinned plugin revision; the live tool response itself already includes
the inventory. At that checkpoint the Monterey/Xcode build and clone gates
were still open; the image build passed on 2026-10-03.

A subsequent Balda request to add Xcode 13.4.1 to the Monterey VM instead
started `virfield_image_build` for the configured `macos-monterey-golden`
recipe. Live job `job-2a45772af03f9514b85f9e95f62c42c8` pinned macOS
12.6 build 21G115 but had no Xcode or developer provisioning in `job.image`.
At `create_dispatched`, it was installing macOS only. This job cannot satisfy
the Xcode request, and the in-flight create must not be interrupted or mutated
blindly. Balda plugin rc.14 now explicitly requires comparing requested Xcode
with the recipe's pinned Xcode before calling `virfield_image_build`, and
reporting the version actually pinned in the returned job. The existing
conversation retains its earlier plugin revision. A true existing-VM Xcode
upgrade API is not implemented; a new combined macOS/Xcode image is required.

The Balda turn ended at 06:08:48 UTC; the Virfield setup error was persisted at
06:12:41 UTC. Image tools have no durable notification subscription, so Balda
did not receive a new turn for that failure. Its earlier calls to
`virfield_image_catalog` and `virfield_image_job` failed with invalid worker
profile and job ID; `virfield_image_create` then failed with `image_exists`.
The bot fell back to the configured macOS-only `image_build` and incorrectly
described it as a macOS+Xcode job. The exact `job.image` had `xcode=null`.
The job's Setup Assistant failure was a light-gray Monterey language arrow
not recognized by the image detector. The detector now matches the observed
60×60 arrow crop; the regression test passes and the idle daemon was updated.
After user confirmation, recovery delete job
`job-fd58124a415221333993640f9e54cd39` succeeded and that failed Monterey
VM was removed. A later combined Monterey/Xcode VM is the current ready image.
Plugin rc.15 also requires explicitly stating that running image jobs have no
automatic status notification; it must not promise a later report until a
durable notification route exists.
The next Mattermost root post created `job-057b0898f3746df8b12fb8b5c4c34c1c`
through Balda. Virfield confirms the immutable profile pins macOS 12.6
`21G115`, Xcode 13.4.1 `13F100`, and `developer-v1` provisioning. Its download
stage immediately reached `apple_auth_required`; no VM existed for that job at
that checkpoint. The same job later produced the ready image.
Balda's first reply showed `queued` and did not send an Apple sign-in link.
The same MM thread must continue through `virfield_image_job` and
`virfield_image_apple_auth` to validate the handoff end to end.

Two subsequent Mattermost requests reached Balda but exhausted its five
retries before a model turn. The first could not resolve `broker` because the
recreated Broker was on a different Docker network. The Broker Compose override
now pins the actual Balda network, and its `broker` name answers from that
network. The second reached the model step, but Docker DNS returned NXDOMAIN
for `api.deepseek.com`. Balda Compose now pins working DNS resolvers; the
recreated container resolves DeepSeek and Broker, reaches DeepSeek over HTTPS,
authenticates to its read-only `/models` endpoint, and reads Mattermost with
its bot token. Both original commands are deadlettered and will not resume
automatically. A successful new Balda turn remains to be observed before
claiming end-to-end acceptance.

Virfield's installed daemon restarted with schema 7 after backup
`backup-cf6c828af990e829161fae269897e26f`; migration created one checked
private database snapshot. A scoped Broker token read the catalog and received
403 on operator `/status`. The pinned patched Lume v4 CLI is installed and
configured for image operations. After explicit administrator authorization,
the root-owned launchd plist was changed to v4 and the system Lume service
restarted. An initial bootstrap error restored the old service; a second
attempt waited for the old port to close and succeeded. `launchctl` reports
the v4 path and a running service, Lume responds on loopback port 7777, and
Virfield reports capacity 0/2 with no blockers after the restart.

The following audit risks remain open:

- Container ingress, TLS and tunnel reachability need an end-to-end test from
  the actual Balda container.
- VS Code remote debugging has fixture coverage for pinned configuration,
  non-overwrite behavior, generic SwiftPM actions and the DuckDuckGo
  `swiftbuild`/host-app/sign/test scheme. The generated adapter, prepare helper,
  host build, signing, tests, deployment and live DAP breakpoint were exercised
  against the large `apple-browsers` worktree.
- Monterey/Xcode image preparation and one disposable legacy clone now passed.
  Big Sur remains untested. The default
  portable policy still reports `disk_encrypted` for Monterey.
- Short image, export and SSH stages can finish and persist their confirmed
  results during the bounded shutdown drain. Graceful restart during a longer
  image mutation requires more implementation and failure injection. The source
  candidate's Recovery flow now passes its password through a private file and
  uses Lume's kernel-assigned VNC port. Go tests cover the fresh session
  endpoint, child arguments and secret-file cleanup; the pinned v5 Swift build
  passed. The installed Lume service remains v4 while the Xcode job runs, so
  live Recovery acceptance of v5 remains open. Virfield's isolated Lume
  registry settings disable
  cache reuse; a regression test pins that setting. Pinned Lume hashes every
  freshly downloaded OCI layer against its SHA-256 digest and records the
  completed manifest digest, which Virfield compares to the accepted digest
  before boot. Lume's optional cache path outside Virfield still reuses layers
  and reassembled images without a fresh hash; that general Lume path is not
  accepted as part of this Virfield deployment.
- Schema 7 created a checked private snapshot on the installed database and
  prunes completed history after 30 days. Pruning passed historical-schema
  tests, but a live 30-day aging interval has not elapsed. Soak,
  second-Mac, VM-disk restore and full Balda workflow acceptance have not run.
  Native Apple website sign-in and cookie transfer resumed the live Xcode job;
  XIP verification and guest installation passed later, while clone acceptance
  remains open.

## Accepted behavior

| Check | Result and evidence |
|---|---|
| Real Apple IPSW download | 26,626,436,228 bytes downloaded from Apple; two daemon restarts exercised HTTP Range resume; pinned SHA-256 passed |
| Clean full-profile image build | `TestLiveImageRebuild`: PASS, 1277.65 s; no manual recovery or mutation retry during this build |
| Full-profile clone execution | `TestLiveLeaseSSH`: PASS, 88.32 s; two clones executed Swift and produced real screenshots |
| Admission and API responsiveness | Third lease refused at 2/2; idempotency passed; maximum API read latency during the clean build was 17.1005 ms |
| SSH isolation and tunnels | Own keys accepted; cross-lease and inherited image keys rejected against each clone's new host pin; tunnel cleanup passed |
| Daemon crash/restart | Real installed-service restart preserved leases; a crash during dispatched SSH preparation quarantined the lease without replay; explicit cleanup passed |
| MCP and browser | Installed HTTP/stdio discovery and status passed; anonymous MCP rejected; desktop/390px console passed with no browser errors |
| Backup integrity | Historical format-1 isolated snapshot copy passed SQLite integrity/foreign keys/schema checks and exact config/token/image-identity comparison; the new token-hash-only format is covered by unit tests, not yet a live restore drill |

The accepted clean image was `image-93604e81b7b84976aa96a86173072f2d`, created by
job `job-da632cf1a2dffeacb8076ccde6e4fd38`. It exercised verified cached media,
fresh installation, unattended setup, remaining Assistant screens, password
rotation, Recovery/SIP, Xcode 27.2 beta (27B5019j), guest tools, Gatekeeper, AMFI,
TCC and sudo. Reboot checks covered password persistence, key-only SSH, Finder,
Swift execution, System Events and actual Peekaboo permissions. Publication
required final confirmed guest shutdown.

The expanded clone run used `lease-23a7ccc51bce7bcf74a1f7c931fb7e36` and
`lease-e222d3c84415c1d221a2ad6f9f74819c`. Both clones and their private identities
were cleaned up. Temporary golden cleanup job
`job-1a29313d808fda7023977a9c42f63d38` succeeded. The original golden had also been
explicitly deleted and rebuilt through the API earlier in acceptance; the current
`macos-27-golden` is verified and stopped.

The two Lume dependency fixes are required: remove a completed guest from the
running cache, and request clean shutdown after successful unattended setup.
The latter eliminated the observed fresh-account password failure in the clean
rerun. The focused cache-release and setup-shutdown regressions passed; applying
the versioned patch with zero fuzz to the pinned archive reproduces the built
Swift sources. Earlier failed development attempts are not counted as passes.

The full-profile acceptance above was collected on commit `44a1c15`.
As checked on 2026-10-01, the installed runtime is candidate `9d0860e`, with
`release_status: registry_candidate_pending_live_acceptance` and schema 6.
The disk gate, 17 MCP tools and embedded agent runbooks are deployed. Installed
HTTP/stdio discovery, authenticated status and anonymous rejection passed.
The live deployment remains outside the checkout at `~/.virfield-v2`;
its `release.json` records the installed commit and binary hashes. The explicitly
authorized schema 5 → 6 update followed integrity and foreign-key checks of
snapshot `backup-ff4570a85e703b595b92a9c2606cf3f3`. VM disk restore was not part of
that check. Subsequent checkout commits are not installed until confirmed by
the deployment manifest; the candidate is not production accepted.

Private development evidence is archived outside the worktree under
`~/.virfield-v2/archives/repository-cleanup-20260930/development-state/`.
The `v2-live-20260929-a/fresh-profile-20260930/` directory contains verification
manifests and an acceptance summary without copied credentials. These private
artifacts are not required to build the repository.

The three unrelated VMs (`macos-15-golden`, `pdf-hud-macos27`,
`uitest-26.4.1-golden`) remained stopped and untouched. Host SIP and Gatekeeper
remain enabled. The previous service and MCP processes were stopped; their
source and build artifacts are now absent from the current worktree. The
previous application remains available in Git history and is not a supported
branch or deployment.

## Automated checks

Run from the repository root:

```sh
make check
make build
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --redact --log-opts=--all
```

`make check` includes documentation links, Python Assistant/Recovery regressions,
deployment path/port/permission checks, gofmt, `go vet` and `go test -race ./...`. Live tests skip without explicit opt-in.
Fixture tests need permission to bind loopback sockets. Tests cover concurrent
admission, external/unknown capacity, journal rollback/reopen, ambiguous effects,
delayed starts, expiry, cleanup/drift, authentication, strict input, SDK tools,
redirect credential protection, downloads, host-key pinning and bounded I/O.

The prior `govulncheck` v1.6.0 run found zero reachable or imported-package
vulnerabilities. One advisory affected an unimported OpenPGP package; that result
is dated acceptance evidence, not a claim about the current vulnerability feed.
Linux cross-compilation passed. Lume-specific regression commands live with its
[dependency recipe](IMAGE-PIPELINE.md#profile-and-dependencies).

The candidate stdio adapter also passed a read-only smoke check from a working
directory outside the checkout: 13 tools, four documentation resources, matching
help/resource content, server instructions and authenticated daemon status.
This is a binary/protocol check, not Codex or Claude workflow acceptance.

## GHCR candidate checks (2026-10-01)

The working candidate has 17 MCP tools, including source discovery, manifest
resolution, import and portable publication. `make check` (including race tests),
`make build` and staticcheck passed. Tests exercise legacy/OCI formats, digest
mismatch, redirect refusal, credential-file permissions, bounded metadata,
imported VM resource/network normalization, durable import replay, portable
rebuild/source protection, interrupted/ambiguous upload and cleanup ordering.
These fixture results do not establish live guest or registry round-trip success.

A read-only public GHCR probe resolved
`ghcr.io/trycua/macos-sequoia-vanilla:latest` to
`sha256:781469ea44dd687f5cfbb130131ea2efee799e34b0fe2a297a0ecab013427266`
with 17,818,752,054 declared compressed bytes. No disk was pulled and no tag was
published in this check. Reproduce with:

```sh
VIRFIELD_LIVE_REGISTRY_METADATA=1 go test ./internal/registry -run '^TestLivePublicManifest$' -count=1 -v
```

Live import of `macos-sequoia-vanilla:15.2` at that digest reached `image_ready`
(job `job-6706c89cabe70b931d162903b6f89ff1`, image
`image-d99fdbe7a626a10f84a265ab297b38b5`, verified 2026-10-01T08:21:19Z).
Guest macOS 15.2 / 24C101, both unencrypted/unlocked boot volumes, rotated
credentials, key-only SSH, Finder and autologin passed. Paired Recovery committed
SIP disable; Gatekeeper, AMFI and Terminal/SSH TCC grants passed after reboot,
including the System Events probe. Final clean shutdown was confirmed. No Xcode
was selected in this import. Ten API reads during download took at most 11.59 ms.

This was a recovered development run, **not a clean full-run acceptance**.
The previous failed import was explicitly deleted (cleanup job
`job-58fd5ef0b56242701d5178eb73cc1a8d`, succeeded; VM absence confirmed).
The clean replacement demonstrated that preserving the native imported account
fixes password rotation and retains Secure Token. It then exposed two additional
issues: moving the cursor after opening Recovery menus could select Apple-menu
items, and headless Sequoia's `sysadminctl -autologin` could report exit status zero
while retaining the old password. The fixed Recovery driver was retried through
operator recovery. The corrected `SecureImage` code repaired and verified the
same guest's private autologin cache before the provisioning retry. The deployed
`9d0860e` includes both fixes and applies cache verification to all new images
and worker clones. Full checks, build and staticcheck passed; gitleaks found no
secrets, and govulncheck found no vulnerabilities in used code/imported packages
(one advisory affects an unused package in a required module).

`TestLiveLeaseSSH` passed on this golden in 55.36 seconds (2026-10-01), using
`lease-54c01094abd7c71b4bde58c715ce0095` and
`lease-568d0c97a002f56a81fadeea55c2373b`. Both ordinary clones booted, reported the
exact macOS/build and unencrypted boot volumes, passed Finder/SIP/Gatekeeper/AMFI
probes, and exported the expected file with SCP. Distinct host pins, own-key
acceptance, cross-lease/image-key rejection, authenticated tunnels, idempotency
and third-lease capacity refusal passed. API responses contained none of the
checked private credentials. Both disposable VMs, private identities and tunnels
were cleaned up; final pool capacity was 0/2 used with no active jobs. The golden
was preserved. Private test output is in the deployment's
`acceptance-sequoia-clones-20261001.log`.
Fresh full import and publish → pull-back → ordinary clone/workload acceptance
passed below; client workflow checks remain release gates. Publication rebuilds a portable
recipe; raw source-disk export and preservation of manual golden changes are not
offered. The first published tag below failed pull-back and is not an accepted image.

The first two clean portable-build attempts on 2026-10-01 did not reach upload.
`job-a56dffdf456ecf269dc7c18bce963a97` downloaded and verified the Apple
15.2 IPSW, then stopped at the Sequoia Accessibility screen: its `Not Now`
control was clipped at 1920×1080. `job-5db0a2a0487122d62f3af0c7504ab46c`
used 1920×1440 and passed Accessibility, then stopped because the Sequoia Apple
Account screen says `Set Up Later`, unlike the earlier macOS 27 screen. Both
quarantined temporary export VMs were deleted through exact-image recovery;
their deletion jobs succeeded, the source golden remained stopped and the pool
returned to 0/2. The agreed GHCR tag remained absent after those attempts.
Commit `30f3d4a` adds the taller display and a screenshot-verified OCR target
for `Set Up Later`.

The third fresh build, `job-c7b629107f813b23f0ff425acd0df21f`, passed
Assistant, guest security and reboot verification, sanitation, GHCR upload,
and temporary VM cleanup. It published
`ghcr.io/mallexxx/virfield-e2e-sequoia152:e2e-20261001-1200-a6cc966` at
`sha256:c500d1206c768c9c720cd0da9ecf6293d7a2a86e543c5555b97e41bfed7b3811`.
The digest and tag resolve consistently, but the OCI manifest is incomplete:
it declares an 80 GiB disk and contains only parts 0–13 of 512 MiB each (7 GiB).
The Lume push path failed to consume errors from its child upload task group,
then silently compacted missing part descriptors when constructing the manifest.
This explains why an incomplete upload could report success; the specific
upload failure remains unknown because it was not propagated. Do not pull or
clone this tag.

Pull-back `job-e67cee0e7659835a600a99f91c47e18f` reached a running guest
without DHCP/SSH and failed `guest_timeout`; its VNC screen was black. The
quarantined imported VM was deleted through exact-image recovery
`job-77f096b2aec8cec10540e73c58833206` after operator authorization.
The source golden remains stopped, the pool is healthy at 0/2 used, and the
incomplete GHCR tag remains available for diagnosis. No round-trip or clone
acceptance is claimed for the published image.

Commit `04c9f13` installed a resolver guard and a pinned Lume patch that rejects
missing disk parts. The follow-up patch also propagates child upload errors.
An 80 GiB synthetic dry-run with 160 parts completed without a missing part;
this exercises compression/collection without GHCR upload and did not prove
the network upload path. The new unique tag and full pull-back are recorded below.

The next clean export (`job-847168d15f89f1744f2efe33296ad419`) reached
paired Recovery but stopped at `sip_dispatched` before upload. Screenshot 005
showed a transient picker with centered Options (OCR x=725); screenshot 006
showed the settled two-icon picker with Options at x=1141. The driver's click
used the transient coordinate after the layout changed and selected Macintosh
HD. The failed job left its temporary VM stopped and the new tag absent.
`recovery.py` now waits for Options in the stable right position and reads the
Continue button ROI without requiring OCR to spell the Macintosh label. The
saved screenshots and OCR confirm the two positions; the later clean build
verified the fix live.

That clean retry (`job-6eb402a6e5d8d4f630d921328f9676e6`) selected the
right-hand Options but again timed out before Continue. The next screenshot
showed Continue under Options; its cropped ROI yielded exact OCR `Continue`
at 96% confidence. The cursor obscured the final letter of the Options label,
so a guard on full-frame `Options` OCR skipped the ROI. The driver now scans the
fixed right-hand Continue ROI on every `options-selected` frame while still
requiring exact button OCR before clicking. The tag remained absent and the
failed temporary VM was stopped. The next clean run verified this second fix.

Clean publish `job-4a0f22bf6531f77de5669742ae476442` then passed native
Assistant, paired Recovery, security/reboot verification, sanitation, GHCR
upload and exact temporary-VM cleanup without operator recovery. The published
tag is `ghcr.io/mallexxx/virfield-e2e-sequoia152:e2e-20261001-165cef8-r2`,
digest `sha256:d9a22e89fef5c73f9ed3241b48e1b40b4ff9eb383a152cce7684b6a4d7b41e72`.
The installed resolver accepted the manifest. A direct GHCR metadata read
confirmed 160 consecutively numbered, contiguous 512 MiB disk parts, covering
the declared 85,899,345,920 bytes exactly; all part-count annotations are 160.
The source `vf-ghcr-sequoia152` remained stopped and preserved.

Pull-back `job-5c0bf72ab62c50355e7eaf94184013b3` pinned that digest and
reached `image_ready` without manual intervention. Its new golden is
`vf-e2e-sequoia152-r2-20261002` (`image-6a2453038d8cffc2d3bfc6481c700996`).
Verification at 2026-10-01T18:59:12Z confirmed macOS 15.2 / 24C101,
`automation`, and FileVault/encryption/lock all false on both boot volumes;
the job also passed SIP, SSH credentials and Finder after reboot. The golden
was stopped normally.

`TestLiveLeaseSSH` passed on this new golden in 67.27 seconds, creating only
disposable leases `lease-a4a01025e370187507229df4996e4460` and
`lease-af2861ac848aca915a42aaa1ff21f7e4`. Both ordinary clones booted,
reported the exact version and unencrypted disks, passed Finder/SIP probes,
accepted their own SSH keys and rejected cross-lease and image keys. Distinct
host pins, tunnels, idempotency, third-VM capacity refusal at 2/2 and real
SCP artifact export passed. Both test clones and their credentials/tunnels
were removed through the API. Final inventory showed 0/2 slots used, no
active jobs, and both original and imported goldens preserved.

## Read-only installed-service checks

```sh
VIRFIELD_LIVE_LUME_URL=http://127.0.0.1:7777 go test ./internal/lume -run '^TestLiveReadOnly$' -count=1 -v
VIRFIELD_LIVE_MCP=1 \
VIRFIELD_LIVE_TOKEN_FILE="$VIRFIELD_HOME/token" \
VIRFIELD_LIVE_MCP_BINARY="$VIRFIELD_HOME/bin/virfield-mcp" \
go test ./internal/mcpadapter -run '^TestLiveInstalledMCP$' -count=1 -v
```

Set `VIRFIELD_HOME` to the existing deployment as described in
[Operations](OPERATIONS.md#paths-and-configuration). These checks do not establish
image or clone correctness on their own.

## Live mutation checks

The opt-in values below acknowledge VM creation and permanent deletion; obtain
operator authorization for the exact target before setting them. Do not run
these tests merely to make CI green. Require healthy idle inventory, no running
VMs, and no unresolved jobs. Never stop unrelated VMs to satisfy prerequisites.

The clean image rebuild command and destructive scope are maintained only in
[Image pipeline](IMAGE-PIPELINE.md#reproducible-live-acceptance).
For two disposable clones of an already verified image:

```sh
VIRFIELD_LIVE_LEASE_SSH=I_APPROVE_TEMPORARY_VM_DELETION \
VIRFIELD_LIVE_TEMPLATE_ID=macos27 \
VIRFIELD_LIVE_TOKEN_FILE="$VIRFIELD_HOME/token" \
VIRFIELD_LIVE_STATE_DIR="$VIRFIELD_HOME" \
go test ./internal/client -run '^TestLiveLeaseSSH$' -count=1 -v -timeout=17m
```

This uses the deployed API and preserves the golden. The full tool probes run
when the selected template configures `uitest-27-v1`. Unknown outcomes remain
quarantined for inspection, not forcibly removed.

The Monterey/Xcode image is ready under the identity-preserving `legacy_uuid`
policy. For its clone check, set `VIRFIELD_LIVE_CLONE_COUNT=1` and
`VIRFIELD_LIVE_TEMPLATE_ID=macos-12.6-xcode-13.4.1` with the same command after
the manager is idle. This creates and cleans up one disposable clone and checks
the exact macOS build, encrypted-at-rest System/Data volumes with FileVault off,
Xcode 13.4.1 / 13F100, Swift compilation, pinned SSH, Finder, SIP and artifact
export. It also checks refusal of a second concurrent legacy worker. The source
image is preserved.

For the lower-level controller lifecycle test, first stop the manager (the host
singleton lock permits only one controller). Use a new private state directory:

```sh
VIRFIELD_LIVE_MUTATIONS=I_APPROVE_TEMPORARY_VM_DELETION \
VIRFIELD_LIVE_STATE_DIR=/absolute/private/new-acceptance-state \
VIRFIELD_LIVE_TEMPLATE=macos-27-golden \
go test ./internal/control -run '^TestLiveLifecycle$' -count=1 -v -timeout=26m
```

## Versioned images (2026-10-01)

| Guest / policy | Evidence |
| --- | --- |
| Monterey 12.6 / 21G115, default protection, no Xcode | IPSW download and native Assistant passed; source boot, key-only SSH, password rotation and Finder passed. **Not accepted in default clone mode:** both ordinary clones stalled before SSH; FileVault is off but System/Data report encryption at rest. The legacy `legacy_uuid` path now permits APFS encryption-at-rest only for identity-preserving images and enforces one active worker at a time, but the full same-identity live path is not accepted. |
| Sequoia 15.2 / 24C101, GHCR import, `automation`, no Xcode | Clean portable publish, complete 80 GiB GHCR manifest, clean pull-back and two ordinary clones passed boot, isolation, capacity, disk, workload/SCP and cleanup. The earlier recovered import separately passed Gatekeeper/AMFI/TCC and reboot verification. |
| Monterey with `automation` security | Implementation present; no live Monterey automation acceptance yet |
| Xcode 13.4.1 / 13F100 on Monterey 12.6 | `image_ready` on 2026-10-03: job `job-057b0898f3746df8b12fb8b5c4c34c1c` succeeded, with exact Xcode build, Apple deep signature, first launch, Swift compilation and reboot checks. A live VirtioFS probe showed no mount on Monterey. The guest verified the transferred XIP checksum, but `xip` refused expansion with 42 GiB free on the existing 80 GB VM. A 160 GB Lume resize dry-run refused encrypted APFS without changing it. The fallback streamed the verified host Xcode.app without macOS tar metadata; guest signature and version checks passed. One Apple Virtualization startup crash interrupted the first verification attempt; an operator retry of verification succeeded. One identity-preserving legacy clone passed boot, Swift compilation, SSH, artifact export and cleanup. |
| Fresh 120 GB Monterey/Xcode image | Job `job-ca072dfb3bfee011bd7ee9fcd28d8f46` succeeded unattended on 2026-10-03. The guest received the verified XIP by SSH, checked its checksum, expanded it without fallback, verified Apple's deep signature and exact version/build, completed first launch and post-reboot checks, then stopped as `image_ready`. Its own clone check is pending. |
| Other catalog macOS generations | Selectable; not yet a claim of live acceptance on this host |

The disk-policy probe in Monterey reports `FileVault=false`, `Encryption=true`,
`Locked=false` for both boot volumes. The earlier default-mode golden was
rejected and deleted through the API (job `job-d8a05d245b9ca804b35e0212a9be87fe`).
The current image is accepted only under the identity-preserving `legacy_uuid`
policy: one active worker at a time and no portable registry publication.
Apple [documents default APFS encryption on Apple silicon even when FileVault
is off](https://support.apple.com/en-nz/guide/security/sec4c6dc1b6e/web).
This matches the guest observation. The first disposable legacy clone attempt
timed out before guest IP readiness because Lume assigned a new
`machineIdentifier` and `macAddress`; its cleanup passed and the source image
remained stopped. After Virfield began restoring both identity fields on the
clone before boot, `TestLiveLeaseSSH` passed in 86.99 seconds on 2026-10-03.
Lease `lease-0f4876238e98c53356e4854e58493931` verified macOS 12.6 /
21G115, encrypted-at-rest boot volumes with FileVault off, SIP enabled,
Xcode 13.4.1 / 13F100, Swift compilation, Finder, scoped SSH and host pin,
image-key rejection, tunnel access and SCP artifact export. A second concurrent
legacy worker was rejected with `legacy_uuid_in_use`. The test deleted its
temporary clone; Lume inventory confirmed it absent, the source image stopped
and `image_ready`, zero active jobs and zero used slots. That first image build
required operator recovery. A fresh build passed without
recovery on 2026-10-03. Its template is
`macos-12.6-xcode-13.4.1-clean-20261003`, lease
`image-3ba4a9985c11ec23a7289023f2fa386f`; Lume reports it stopped with a
120 GiB virtual disk and 34.8 GiB allocated on the host. The first clean
attempt used catalog `image-create`, which cannot set `legacy_uuid`, and failed
closed at `disk_encrypted` before Xcode transfer. Its quarantined VM was deleted
through `image-recover`; the successful run used the operator's `config.json`
template with the legacy UUID. The original 80 GB ready image remains stopped.

The initial Monterey bring-up required inspected operator recovery while native
UI and older OpenSSH compatibility were being fixed. The fresh 120 GB build is
the clean-run acceptance record. Older OpenSSH needs both
`KbdInteractiveAuthentication no` and `ChallengeResponseAuthentication no`;
checking just `PasswordAuthentication` does not establish key-only access.

## Not yet accepted

- Remaining VM-mutation crash points, prolonged soak and VM-disk disaster recovery. Unit/mock checks and state snapshots do not replace
  these exercises.
- Registry pull, portable publication/pull-back and macOS builds outside the versioned-image matrix above.
- Container-facing routing/TLS and per-principal authentication.
- Broker, Runner, Callee/provider installation, Balda and Prism integration.
  The complete task → VM → execution → result → cleanup workflow from Balda
  has not passed end-to-end acceptance.

## Portable release checks (2026-09-30)

The source archive passed `make check`, `make build` and `go mod verify` with an
empty HOME and separate empty Go module/build caches. A fresh Python 3.14 VNC
environment installed the pinned requirements and passed imports and `pip check`.
Deployment tests cover a different account/HOME, paths with spaces, custom ports,
insecure token refusal and missing tools. Archive smoke checks also exercised
initialization, plist/MCP generation, refusal to overwrite and the Lume wrapper's
configured port, without starting a real VM service.

Gitleaks 8.30.1 found no secrets in the complete reachable Git history or extracted
release contents. An additional local comparison against current deployment
credentials found no matching Git blobs. Go vulnerability checks found no
reachable or imported-package vulnerabilities. Archive checks verified binary
hashes, ad-hoc signatures and absence of the author's HOME path in all four
binaries. Secret scanning is evidence, not a guarantee; `.gitignore` and the CI
scan guard future changes as well.

GitHub [Checks](https://github.com/mallexxx/virfield/actions/workflows/check.yml)
runs the Linux/macOS matrix. The macOS job now builds the pinned Lume patch set,
executes its four focused Swift regression tests, and verifies the signed
binary/version; this CI change has not yet run on GitHub. Publication requires
both jobs to pass for the exact release commit. This portability check does not claim a full VM image build on
a second physical Mac. The image acceptance above remains tied to its stated
hardware/OS/tool versions.

## Release procedure

Using Python 3.12 or newer, after checks pass on a clean committed tree, build an archive with
`python3 tools/release.py v2.0.0 --output /absolute/new/release-directory`.
The packager uses only committed source, freshly built macOS/arm64 binaries and
the signed native Apple browser. It normalizes archive timestamps, owners and
entry order, and includes dependency notices, file hashes and the exact Git revision. It
refuses dirty source, a mismatching existing version tag or existing outputs.
Inspect/extract the archive, scan it for secrets, verify its checksum and smoke-test
CLI initialization and service preparation outside the original checkout.

Push only the intended branches; require successful GitHub Actions checks for the
exact main commit before creating the release tag and publishing the archive plus
`SHA256SUMS`. Never include private deployment state or local acceptance logs.
