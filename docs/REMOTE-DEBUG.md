# VS Code remote debugging

Virfield generates a host-build/remote-run workflow for a ready macOS lease.
Install the official LLDB-DAP VS Code extension before opening the generated
workspace.

## Data path

| Operation | Location and protocol |
|---|---|
| Swift build and tests | Host, using the repository's existing commands |
| App and debug products | Incremental `rsync` over pinned SSH |
| Process launch | Apple `debugserver` bound to guest loopback |
| Debug connection | Host `lldb-dap` and gdb-remote through an SSH tunnel |
| macOS system images | Xcode `macOS DeviceSupport`, prepared from read-only NFS during golden verification |

The debug port is never exposed on the VM network. Golden verification extracts
the guest cache into `~/Library/Developer/Xcode/macOS DeviceSupport/<version>
(<build>)/Symbols`. The generated helper validates that cache, mounts NFS only
as a recovery fallback, starts `debugserver`, and creates the local SSH tunnel.
LLDB loads application symbols on demand and uses minimal remote module loading.

Golden developer images enable `DevToolsSecurity`, add `lume` to `_developer`,
and export only the immutable dyld cache to the private `192.168.64.0/24`
network. Ad-hoc signed apps therefore launch under `debugserver` without an
interactive authorization prompt.

## Create the workspace

Acquire a lease, wait for `ready`, and create its pinned SSH config. Then run:

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
VIRFIELD_URL="http://127.0.0.1:7780"
"$VIRFIELD_HOME/bin/virfield" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token" \
  vscode-config LEASE_A /absolute/private/IDENTITY_A /absolute/path/to/SWIFTPM_WORKTREE \
  LEASE_B /absolute/private/IDENTITY_B
```

Existing files are never overwritten. A DuckDuckGo worktree containing
`DuckDuckGo-macOS.code-workspace` gets:

- `.vscode/DuckDuckGo-macOS.virfield-remote-SET_ID.code-workspace`
- one LLDB prepare helper per supplied ready VM
- a host `lldb-dap` adapter

The Run/Debug dropdown contains one entry per supplied VM, named with both its
VM and lease ID. **Terminal → Run Task → Virfield: Open console** opens the
Virfield web console; each VM also has an **Open VM terminal** task. Files are
lease-set-specific, so creating the next session cannot silently retarget an
open debugging session or overwrite a teammate's configuration.

Open the generated workspace, select **DuckDuckGo — Virfield VM: VM [LEASE]**,
set the desired breakpoint, and press F5. That single action uses
the repository's host-side
`make-host-app.sh`, SwiftBuild `DuckDuckGoBrowserDynamic` product, ad-hoc
signing, and `sign-dylib.sh`; only the resulting app and Debug products are
copied to the VM. **Virfield: Test** runs the existing SwiftPM test command on
the host.

For another SwiftPM package, Virfield creates `launch.json`, `tasks.json`,
`settings.json`, and the same two helpers. Select **Virfield: Debug SwiftPM
product**, press F5, and enter the executable product name.

The verified DuckDuckGo baseline on macOS 27.0/Xcode 27.0 connects LLDB-DAP in
0.14–0.15 seconds and reaches `DuckDuckGoBrowserMain` in a median 7.56 seconds
after the cached prepare step. A missing or incomplete DeviceSupport extraction
blocks golden verification instead of publishing a slow debugging image.
