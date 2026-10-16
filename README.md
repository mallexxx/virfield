# Virfield

Virfield gives your AI agent temporary macOS machines for builds, tests and
debugging. Describe a task in Codex or Claude Code; the agent gets a machine,
works over SSH and saves the results. Virfield manages images, access, lease
deadlines and cleanup.

## What you can do

- Build and test a project on the macOS and Xcode versions it needs.
- Run UI tests on prepared machines and open their desktops.
- Create reusable images from approved macOS/Xcode profiles.
- Import and publish Tart images through a configured GitHub registry (GHCR), or transfer them between storage locations.
- Lease connected physical macOS/Linux hosts.
- Check available resources, extend a machine's lease and diagnose startup failures.

The agent runs build and test commands over SSH through its own terminal.
Available systems depend on your configured images and hardware.

## Install

Local macOS VMs require an Apple Silicon Mac running macOS 13 or later.
Install the Go version in [go.mod](go.mod), Python 3, Xcode developer tools and
[Tart](https://tart.run/quick-start/). Image builds also need
[Packer](https://developer.hashicorp.com/packer/install).

### 1. Clone and build

```sh
git clone https://github.com/mallexxx/virfield.git
cd virfield
make check
make build
```

### 2. Create a deployment

If Virfield is already installed and running, skip to MCP setup and use your
existing deployment path. For a new installation:

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
./bin/virfield init "$VIRFIELD_HOME" macos-golden macos-golden local
mkdir "$VIRFIELD_HOME/bin"
cp bin/virfield bin/virfieldd bin/virfield-mcp "$VIRFIELD_HOME/bin/"
cp -R bin/VirfieldAppleBrowser.app "$VIRFIELD_HOME/bin/"
```

In `$VIRFIELD_HOME/config.json`, configure storage, approved base images,
Packer and the Xcode cache using the [configuration guide](docs/OPERATIONS.md#configure-storage-and-credentials).
`init` creates configuration and a token; it does not download a macOS image.
External storage may need Full Disk Access. Building with
`make build SIGN_IDENTITY="YOUR_SIGNING_IDENTITY"` keeps that grant across rebuilds.

### 3. Start Virfield

```sh
"$VIRFIELD_HOME/bin/virfield" config-check "$VIRFIELD_HOME/config.json"
"$VIRFIELD_HOME/bin/virfieldd" -config "$VIRFIELD_HOME/config.json"
```

Leave this terminal open. For automatic startup, follow the
[service setup instructions](docs/OPERATIONS.md#install).

## Connect your MCP client

Open a second terminal on the same Mac and set your deployment path and daemon URL:

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
VIRFIELD_URL="http://127.0.0.1:7780"
```

Choose your client.

**Codex:**

```sh
codex mcp add virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
codex mcp get virfield
```

**Claude Code:**

```sh
claude mcp add --transport stdio --scope user virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
claude mcp get virfield
```

For another MCP client, use the [stdio configuration example](deploy/mcp.json.example).
Pass the token file's path, keeping its contents private. Reconnect MCP in your
client and open a new chat.

## What to ask your agent

Check the connection:

> Read Virfield's instructions with virfield_help, then check virfield_status.
> Show available machines and images. If setup is incomplete, explain what's missing.

Build and test a project:

> Use Virfield to get macOS 12.7.6 with Xcode 13.4.1 for two hours.
> Copy this project there, run its tests and save the report in my project.
> Once the results are saved, delete only the machine created for this task.

Prepare an image:

> Use Virfield to create a new macOS image with the Xcode version this project needs.
> Check available versions and disk space first. If no approved base matches, tell me.

See what's happening in a VM:

> Open the desktop of the Virfield machine running my tests.

Include the required versions, project, time budget and outputs to save.
You can authorize cleanup in the same request, as in the build example above.

## Detailed guides

- [Installation, nodes, backups and recovery](docs/OPERATIONS.md)
- [Image creation and transfer](docs/IMAGE-PIPELINE.md)
- [MCP tools and agent workflows](docs/AGENT-GUIDE.md)
- [Environment requirements](docs/ENVIRONMENT.md)
- [VS Code debugging](docs/REMOTE-DEBUG.md)
- [Development and release packaging](docs/ARCHITECTURE.md)
