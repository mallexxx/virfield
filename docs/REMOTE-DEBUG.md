# Remote debugging

Virfield generates a host-build/remote-run workspace for a ready macOS VM lease.
Install the official LLDB-DAP VS Code extension and a host Xcode with `lldb-dap`.
Use a developer image matching the guest OS and generate its pinned SSH config.

```sh
virfield -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token" \
  vscode-config LEASE_ID /absolute/private/identity /absolute/path/to/SWIFTPM_WORKTREE
```

Open the generated configuration, select **Virfield: Debug SwiftPM product**,
press F5 and enter the executable product name. The tasks build on the host,
sync products with `rsync` over pinned SSH and launch guest-loopback `debugserver`.
Host `lldb-dap` connects through an SSH tunnel. The debug port stays off the VM
network. The helper waits for the exact debugserver process to listen without
consuming its first debugger connection.

The guest developer image prepares its immutable dyld cache in Xcode DeviceSupport.
The helper validates those symbols and uses read-only NFS only as a recovery
fallback. Missing/incomplete symbols block preparation. Developer qualification
also enables the permissions needed to launch ad-hoc signed apps.

## DuckDuckGo workspace

A checkout containing `DuckDuckGo-macOS.code-workspace` gets a separate
`.vscode/DuckDuckGo-macOS.virfield-remote-SET_ID.code-workspace`. Supply additional
`LEASE_ID IDENTITY_DIRECTORY` pairs to include multiple VMs. Each Run/Debug entry
names its VM and lease. Generated files are lease-specific and never overwritten.

Select **DuckDuckGo — Virfield VM: VM [LEASE]** and press F5. The generated tasks
use the checkout's `make-host-app.sh`, `DuckDuckGoBrowserDynamic` product and
`sign-dylib.sh`, then copy app/debug products to the VM. **Virfield: Test** runs
the existing host SwiftPM test command. **Virfield: Open console** and **Open VM
terminal** open the control plane and pinned VM shell.

Check a breakpoint, stack, evaluation and application output for your project.
A protocol connection alone does not establish editor UI or application behavior.
Export needed artifacts before releasing the lease.
