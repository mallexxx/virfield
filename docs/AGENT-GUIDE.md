# Agent guide

Start with `virfield_help({})`, then `virfield_status({})`. Virfield manages
infrastructure; execute commands, transfer inputs and collect artifacts over
pinned SSH using your shell or Runner.

## Discover instructions

| Topic | Contents |
| --- | --- |
| `workflows` (default) | This guide and tool arguments |
| `images` | [Image pipeline](IMAGE-PIPELINE.md) |
| `operations` | [Operations](OPERATIONS.md) |
| `verification` | [Environment checks](ENVIRONMENT.md) |

The same documents are resources at `virfield://docs/TOPIC`. Request the matching
help topic to follow a document link from MCP. Help reads the running adapter's
embedded files without contacting the daemon. Check status for actual inventory.
Reconnect the owning MCP client after replacing its adapter binary.

## Tool reference

Tool names may have a client-specific prefix. Arguments are JSON.
`idempotency_key` is 8–128 printable ASCII characters; save one key and request
body per logical mutation. IDs/names are 1–80 characters, starting with an ASCII
letter or digit, followed by letters, digits, `_`, `.` or `-`.

| Tool | Arguments | Result / next action |
| --- | --- | --- |
| `virfield_help` | `{"topic":"workflows"}`; topic optional | Read the requested embedded runbook |
| `virfield_status` | `{}` | Inventory, freshness/error, capacity, leases, active jobs, templates |
| `image_catalog` | `{}` | Approved Tart bases, public version metadata and stable Xcode versions/minimum macOS; each Xcode has `local_state` (`available`, `downloaded`, `installed`) and ready `installed_images` |
| `image_create` | `{"id":"NEW_TEMPLATE_ID","macos":"VERSION_OR_BUILD_OR_CODENAME","xcode":"EXACT_VERSION","security":"automation","location":"CONTAINER_ID","idempotency_key":"NEW_KEY"}` | Select an approved Tart base and SIP policy; Xcode is optional; `toolchain: "legacy-ios-v1"` opts into the pinned legacy profile; resolved versions/digests are pinned |
| `registry_sources` | `{}` | Configured GHCR namespaces, authentication availability and publish permission; no credentials |
| `registry_resolve` | `{"source":"SOURCE_ID","repository":"PACKAGE","tag":"TAG"}` | Resolve an allowlisted GHCR Tart manifest and verify its digest/chunk metadata |
| `image_pull` | `{"id":"NEW_TEMPLATE_ID","source":"SOURCE_ID","repository":"PACKAGE","tag":"TAG","macos":"EXPECTED_VERSION_OR_BUILD","xcode":"EXACT_VERSION","security":"automation","location":"CONTAINER_ID","idempotency_key":"NEW_KEY"}` | Pin the Tart manifest, rebuild/qualify the expected OS and policy, preserve provenance |
| `image_publish` | `{"template":"TEMPLATE_ID","source":"SOURCE_ID","repository":"PACKAGE","tag":"NEW_TAG","idempotency_key":"NEW_KEY"}` | Fresh portable Tart rebuild → sanitize → stopped push → digest verification → confirmed temporary cleanup |
| `image_build` | `{"id":"TEMPLATE_ID","idempotency_key":"NEW_KEY"}` | Build an absent VM from its registered profile; poll `job.id` |
| `image_transfer` | `{"template":"SOURCE_TEMPLATE","id":"NEW_ID_AND_NAME","location":"DESTINATION_CONTAINER","idempotency_key":"NEW_KEY"}` | Copy a verified local Tart golden to different storage; idle pool, full disk budget, source preserved, hashes verified before registration; poll `job.id` |
| `vm_acquire` | `{"template":"TEMPLATE_ID","ttl_seconds":3600,"ssh_public_key":"ssh-ed25519 PUBLIC_KEY","destination_location":"fast","idempotency_key":"NEW_KEY"}` | Durable operation; `destination_location` is optional and strict; omission uses configured clone storage fallback order |
| `virfield_job` | `{"id":"JOB_ID"}` | Job state, phase, progress, deadline and error |
| `image_apple_auth` | `{"id":"JOB_ID"}` | Local Apple sign-in for the exact failed Tart Xcode resolve job; no credentials in tool arguments |
| `vm_lease` | `{"id":"LEASE_OR_IMAGE_RECORD_ID"}` | Durable record; a ready worker includes `ip`, `ssh`, `expires_at` |
| `vm_revalidate` | `{"id":"LEASE_ID","vm_name":"EXACT_VM_NAME","caller_proof":"LOCALLY_SIGNED_PUBLIC_PROOF","idempotency_key":"SAME_PROOF_KEY"}` | Read-only in-place recovery of a drifted unexpired VM using its original runtime/image/SSH identities; generate proof with `virfield -key SAME_PROOF_KEY revalidate-proof ID VM_NAME EXISTING_IDENTITY_DIRECTORY`; poll job and lease; no key generation or deletion |
| `virfield_events` | `{"after":0,"lease_id":"LEASE_OR_IMAGE_RECORD_ID"}`; both optional | Ordered events and `next_cursor`; persist cursor after processing |
| `vm_renew` | `{"id":"LEASE_ID","expires_at":"ABSOLUTE_RFC3339_TIMESTAMP"}` | Updated lease; deadline must be in the future, at most 24 hours ahead |
| `vm_tunnel` | `{"id":"LEASE_ID","service":"ssh"\|"desktop"}` | `{lease_id,address,service,url}`; address is loopback on the daemon host; a `desktop` tunnel (VM leases only) returns `url` `vnc://admin:<lease password>@127.0.0.1:<port>` for Screen Sharing |
| `vm_tunnel_close` | `{"id":"LEASE_ID","service":"ssh"\|"desktop"}` | Close that service's tunnel and its active connections (`ssh` by default) |
| `vm_release` | `{"id":"LEASE_ID","idempotency_key":"NEW_RELEASE_KEY"}` | Permanent clone deletion; poll returned job to completion |

`template` identifies golden configuration. `image-…` is an image operation record,
`lease-…` is a worker lease, and `job-…` is an asynchronous job. `vm_name` is the
backend VM name used for exact-target confirmation. Keep these identities distinct.

## Build a golden

Read status and the `verification` topic. Match the required OS/build, architecture,
Xcode and security policy to an approved Tart base or configured node profile.
`image_create` selects a pinned Tart profile; `image_build` builds an absent
configured golden. Require job `succeeded` and image record `image_ready` before
leasing. Run workloads on disposable clones.

Catalog metadata describes available versions. It does not prove that a download
is authorized or that a workload works. Report an unmatched profile instead of
substituting versions. See the images topic for Apple sign-in and recovery.

## Acquire, execute and export

1. Check inventory freshness and capacity. `resource_admission[TEMPLATE]` explains
   CPU/RAM/storage/cache limits; dispatch rechecks them. Queueing belongs to the
   caller. TTL is 60–86400 seconds and includes boot and preparation.
2. Generate a new private Ed25519 identity outside Git. Submit only its public key
   to `vm_acquire`; save the returned job/lease IDs and request key.
   `destination_location` is strict. Omission uses configured default/fallback
   storage; a local golden still requires compatible source storage.
3. Poll the job, then read the lease. Require `state == ready`, an IP and verified
   `ssh` metadata. Generate pinned connection files with the CLI below.
4. Run work in `~/workspace` for VMs or `~/virfield/leases/VM_NAME/workspace` for
   physical hosts. Export outputs before expiry. Renew with an absolute RFC3339
   timestamp, at most 24 hours ahead; renewal cannot shorten the deadline.
5. With cleanup authorized, call `vm_release` using its own stable key. Require job
   `succeeded` and lease `released`. A failed job or expiry alone does not free
   capacity: the controller must confirm cleanup.

These commands run on the daemon host. Replace placeholders with returned IDs
and actual deployment paths; the CLI reads the token file itself.

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
VIRFIELD_URL="http://127.0.0.1:7780"
IDENTITY_DIR="$HOME/.local/share/virfield-identities/task-42"
umask 077
mkdir -p "$(dirname "$IDENTITY_DIR")"
"$VIRFIELD_HOME/bin/virfield" keygen "$IDENTITY_DIR"
cat "$IDENTITY_DIR/id_ed25519.pub"
# Submit vm_acquire, then wait for readiness.
"$VIRFIELD_HOME/bin/virfield" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token" ssh-config LEASE_ID "$IDENTITY_DIR"
ssh -F "$IDENTITY_DIR/config" virfield 'cd ~/workspace && sw_vers'
scp -F "$IDENTITY_DIR/config" ./input.tar virfield:workspace/input.tar
scp -F "$IDENTITY_DIR/config" virfield:workspace/results.tar ./results.tar
```

`keygen` requires a new directory. `ssh-config` verifies the caller identity and
writes the host pin once; it refuses overwrites. Keep these files through export
and cleanup. Do not disable host checking or use bootstrap passwords for SSH.

VMs use the standard `admin/admin` console account, separate management/caller
keys and fresh host keys. Preparation preserves keychain items and verifies native
GUI and SSH key storage plus an unlocked console. A standard console password
is not a tenant-isolation boundary. Physical hosts share their configured account;
release revokes the lease key and removes its workspace but does not kill workloads.

## Desktop and recovery

`vm_tunnel` with `service: desktop` returns a private Screen Sharing URL for a
ready VM. Its address is loopback on the daemon host. Physical-host leases have
no desktop tunnel. Release, expiry or verified drift revokes access.

For `needs_attention` / `vm_drift`, stop executing and inspect events. An owned,
unexpired VM with a complete verified baseline can use `vm_revalidate` with proof
from the original private identity and the same request key. It checks the
existing VM without preparing it, rotating keys or extending expiry. Wait for
job success and lease readiness before reconnecting. Physical hosts and leases
without the baseline cannot use this operation.

`virfield_events` returns a cursor and bounded typed drift/backend/tunnel
diagnostics. Save `next_cursor`; IDs can have gaps after retention. An error
fingerprint helps correlate private logs but does not identify an unknown cause.
See [Operations](OPERATIONS.md#recovery-and-diagnostics) for recovery commands.

## Handle errors

| Error | Action |
| --- | --- |
| `capacity_exhausted`, `resource_exhausted`, `image_cache_exhausted` | Inspect `error.admission`, blocked storage and reservations; wait or report the shortage |
| `idempotency_conflict` | Restore the original body/key; use a new key only for a new logical operation |
| `backend_unavailable`, stale inventory | Wait for fresh inventory; preserve verified identities and active work |
| `vm_drift`, host-key/authentication failure | Stop work and inspect the owned lease; preserve host pins |
| `guest_keychain_failed` | Preserve private diagnostics before cleanup; follow [keychain diagnosis](OPERATIONS.md#vm-keychain-readiness) |
| `lease_desktop_failed` | Inspect the authenticated guest RFB service; a listener alone does not prove readiness |
| `outcome_unknown`, `operation_timeout`, `unsafe_retry` | Preserve the reservation and inspect the exact VM before recovery |
| Unsupported OS, policy or profile | Select an approved matching profile or report the requirement |
| `lease_expired`, `lease_released` | Export remaining diagnostics and confirm cleanup; acquire a new worker for new work |

Image deletion, cleanup resolution, provisioning, backups and service installation
are operator CLI actions. Require authorization for destructive cleanup and exact
VM confirmation. Never edit SQLite or runtime directories to bypass the journal.
