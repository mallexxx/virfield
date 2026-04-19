# 🧚‍♀️ virfield

macOS VM lifecycle console and MCP server for AI-driven test automation.
Build a golden image once, clone it per test run — managed from a web UI or
directly by Claude Code / Cursor via MCP tools.

---

## Features

- **VM Library** — list, start, stop, clone, delete lume VMs at a glance
- **Golden image pipeline** — four-phase build (create → setup → disable SIP → provision)
  orchestrated via shell scripts with live log streaming
- **GHCR push/pull** — push golden images to GitHub Container Registry; pull on
  any machine with a single click; collision-safe (won't overwrite existing VMs)
- **Settings** — configure paths, GitHub credentials, and GHCR sources through the UI
- **MCP server** — expose all console operations to Claude Code / Cursor as tools
  (`vm_list`, `vm_start`, `vm_ssh_exec`, `peekaboo_see`, `run_tests`, …)
- **lume serve management** — start / stop / restart lume serve from the UI;
  lume runs detached so server restarts don't kill running VMs
- **Backend restart** — restart the Node.js server from the UI without touching VMs

---

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| macOS (Apple Silicon) | VMs require Apple's Virtualization framework |
| [lume](https://github.com/trycua/cua) v0.3.9+ | `brew install trycua/tap/lume` |
| Node.js 20+ | `brew install node` |

---

## Quick Start

```bash
# 1. Clone and install
git clone https://github.com/mallexxx/virfield
cd virfield
npm install

# 2. Start the backend (hot-reloads on changes)
npm run dev

# 3. Start the UI (in a separate terminal)
npm run dev:ui

# 4. Open in browser
open http://localhost:5173
```

The backend runs on **port 3000**, the Vite dev UI on **port 5173**.
For production: `npm run build && npm start`.

---

## Configuration

All paths and credentials can be configured through **Settings** (⚙ in the top-right)
or via environment variables.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VMCONSOLE_SCRIPTS_DIR` | `~/Developer/virfield/scripts` | Path to the build pipeline scripts |
| `VMCONSOLE_LOG_BASE` | `~/Developer/virfield/logs` | Root for build log dirs + state.json files |
| `VMCONSOLE_STATE_DIR` | `~/Developer/virfield/state` | Per-VM state JSON directory |
| `VMCONSOLE_RECORDINGS_DIR` | `~/Developer/virfield/recordings` | VNC recording output |
| `VMCONSOLE_VMSHARE` | `~/VMShare` | virtiofs shared volume path |
| `VMCONSOLE_REPO_DIR` | `~/Developer/repo` | Repo with `build-for-testing.sh` |
| `VMCONSOLE_DB_DIR` | `~/.virfield` | SQLite database location |
| `PORT` | `3000` | HTTP server port |

Settings saved through the UI are stored in SQLite and take precedence over
environment variables.

---

## VM Build Scripts

The `scripts/` directory contains the golden image build pipeline:

| Script | Description |
|--------|-------------|
| `build-golden-vm.sh` | Orchestrates the full four-phase build |
| `01-create-vm.sh` | Phase 1 — create VM from IPSW |
| `02-setup-assistant.sh` | Phase 2 — run Setup Assistant via VNC |
| `03-disable-sip.sh` | Phase 3 — disable SIP via VNC recovery mode |
| `04-provision-vm.sh` | Phase 4 — install tools, configure macOS |
| `vm-setup.sh` | Individual VM configuration steps |
| `vm-start-peekaboo.sh` | Start Peekaboo screen-capture agent in VM |
| `vnc-record.py` | Record VNC session to video |
| `vnc-send-keys.py` | Send keystrokes to VM over VNC |
| `*.yaml` | Setup Assistant preset files (macOS version-specific) |

Configure the scripts directory in **Settings → Folder Paths → Scripts Dir**,
or set `VMCONSOLE_SCRIPTS_DIR`.

---

## MCP Server

The MCP server exposes the console to Claude Code, Cursor, and any MCP-compatible
client as a set of tools.

### Setup

```bash
# 1. Copy the example config
cp mcp.json.example mcp.json

# 2. Edit mcp.json — replace the placeholder path with your actual path
#    "args": ["tsx", "/absolute/path/to/virfield/server/mcp-server.ts"]
```

Add to your Claude Code or Cursor MCP config, then reload the client.

### Available Tools

| Tool | Description |
|------|-------------|
| `vm_list` | List all VMs with status |
| `vm_status` | Get status of a specific VM |
| `vm_start` / `vm_stop` | Start or stop a VM |
| `vm_build_golden` | Run the full golden build pipeline |
| `vm_clone_golden` | Clone the golden VM for a test session |
| `vm_run_stage` | Run a single build pipeline stage |
| `vm_ssh_exec` | Execute a command via SSH in a VM |
| `vm_prepare_session` | Clone golden + boot + wait for SSH |
| `peekaboo_see` | Screenshot + describe the VM screen |
| `peekaboo_image` | Raw screenshot as base64 |
| `peekaboo_click` / `peekaboo_type` / `peekaboo_scroll` | UI interactions |
| `peekaboo_hotkey` | Send keyboard shortcuts |
| `ax_snapshot` | Capture accessibility tree |
| `ax_diff` / `ax_diff_last` | Diff accessibility snapshots |
| `run_tests` | Run xcodebuild UI tests in the VM |
| `get_test_results` | Parse xcresult for pass/fail/errors |
| `get_log_stream` | Tail unified log output |
| `get_crash_reports` | Retrieve crash logs from the VM |

---

## GHCR Push / Pull

### Push a golden image

1. Open **Settings** → add a GHCR source (registry + org, e.g. `ghcr.io/your-org`)
2. Set your **GitHub Username** and a **PAT** with `write:packages` + `read:packages` + `repo`
3. Go to **Golden Images** → click **↑ Push to GHCR** next to the VM
4. Choose image name, tag, optional extra tags → push

### Pull an image

1. Open **VM Library** → click **⬇ Pull VM**
2. Select a source → choose image and tag from the dropdown (populated from GHCR)
3. Enter a local VM name — the console checks for collisions before pulling
4. Pull runs in the background with live log output

For public images (visibility = public on GHCR) no token is needed to pull.

---

## Database

The SQLite database lives at `~/.virfield/state.db` (configurable via
`VMCONSOLE_DB_DIR`). It stores VM metadata, build job history, GHCR sources,
settings, and accessibility snapshots. It is not committed to the repository.

---

## Development

```bash
npm run dev        # backend with hot-reload (tsx watch)
npm run dev:ui     # Vite UI dev server (port 5173)
npm run build      # production build (TypeScript + Vite)
npm start          # run production build
npm run mcp        # run MCP server (stdio)
```

TypeScript is checked with:
```bash
npx tsc -p tsconfig.server.json --noEmit
```

---

## Architecture

```
server/
  index.ts           Express app + startup
  config.ts          Env-var configuration
  db.ts              SQLite schema + helpers  (~/.virfield/state.db)
  lume.ts            lume HTTP API + CLI wrappers
  tasks.ts           In-memory background task registry
  ssh.ts             SSH exec helpers
  tunnel-manager.ts  socat peekaboo port tunnels
  ax-tools.ts        Accessibility tree helpers
  stages.ts          Build pipeline stage constants
  mcp-server.ts      MCP stdio server
  routes/
    vms.ts           VM CRUD + stage runner
    build.ts         Golden build pipeline
    storage.ts       Storage, IPSW, Xcode
    settings.ts      App settings CRUD
    ghcr.ts          GHCR push/pull + package listing
    ws.ts            WebSocket log streaming

scripts/
  build-golden-vm.sh      Full four-phase golden image pipeline
  01-create-vm.sh         Phase 1: create VM
  02-setup-assistant.sh   Phase 2: Setup Assistant via VNC
  03-disable-sip.sh       Phase 3: disable SIP
  04-provision-vm.sh      Phase 4: provision tools
  vm-setup.sh             VM configuration helper
  vnc-record.py           VNC session recorder
  vnc-send-keys.py        VNC keyboard input sender
  *.yaml                  Setup Assistant presets

ui/src/
  App.tsx                    Root + tab nav
  components/
    VMLibrary.tsx            VM list + Pull VM button
    VMCard.tsx               Per-VM card
    VMWizard.tsx             New VM wizard
    GoldenPanel.tsx          Golden images + Push to GHCR
    GHCRPushModal.tsx        Push modal with live log
    PullVMModal.tsx          Pull modal with image/tag chooser
    SettingsPanel.tsx        Settings + server controls
    StageRunner.tsx          Build stage runner
    IPSWPanel.tsx            macOS / IPSW management
    XcodePanel.tsx           Xcode management
    StoragePanel.tsx         Storage locations
    RecordingsTab.tsx        VNC recordings
    ScreenshotsTab.tsx       Screenshots
    LogViewer.tsx            Build log viewer
```
