# Remote Nodes R2: macOS VMs on the HTPC — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lease disposable macOS 14/15 VMs (SIP disabled) running as rootless Podman containers on the Linux HTPC node, with the same lease contract as Tart: Virfield-secured golden, per-lease SSH identity, TTL, release destroys the clone.

**Architecture:** The bare-metal node runtime (`internal/baremetal`) gains a second lease kind, *node VM*: a qcow2 overlay in `~/virfield/leases/<vm>/storage` over a read-only golden on the node HDD, started as a `dockurr/macos` container that publishes guest port 22 on a per-lease node port. Virfield's own guest SSH always tunnels through the pinned node SSH connection to `127.0.0.1:<port>`, so the image-securing VM is never published on the LAN. A node image job clones the hand-built *bootstrap* golden (admin/admin), runs the existing `guestssh` secure flow, verifies after reboot, then commits the result as a new read-only *secured* golden that leases clone. The SSH endpoint becomes `lease.IP` + `lease.SSHPort` end to end (controller drift checks, tunnel, client ssh-config).

**Tech Stack:** Go 1.x (`golang.org/x/crypto/ssh`), POSIX sh node scripts, rootless Podman 5, `dockurr/macos@sha256:08d1bcaac74ad44d548b7ce683b3dded083512d2a0d26416f9a620b763f8ea3d`, qemu-img (from that image).

**Spec:** `docs/superpowers/specs/2026-10-08-hackintosh-nodes-r2-design.md`

---

## File structure

| File | Responsibility |
|---|---|
| `internal/domain/model.go` | `VM.SSHPort`, `Lease.SSHPort` |
| `internal/domain/image.go` | `NodeSource` image source and its validation |
| `internal/control/controller.go`, `tunnel.go` | Copy and compare `SSHPort` with `IP` (readiness, drift, tunnel) |
| `internal/control/image_identity.go` | Fingerprint includes the node source |
| `internal/control/image_build.go` | Node image phase chain `build→qualify→stop→commit`, node capacity for image builds |
| `internal/guestssh/client.go`, `lease.go` | Per-lease port, injectable dialer, accepted guest models; `Prepare` returns the lease port |
| `internal/client/identity.go` | ssh-config with the lease port |
| `internal/config/config.go` | `Node.VMs` block and validation |
| `internal/baremetal/vmscripts.go` (new) | Fixed node scripts for VM probe/clone/start/stop/delete/commit |
| `internal/baremetal/vm.go` (new) | VM lease and image operations, guest dialer, probe parsing for VMs |
| `internal/baremetal/runtime.go`, `ssh.go` | Dispatch host lease vs VM lease; capacity; `Prepare` delegation |
| `internal/nodeimage/builder.go` (new) | `ImageBuilder` for node images: build (clone bootstrap), qualify (secure, reboot, verify), stop, commit |
| `internal/fleet/fleet.go` | Route `Forget`/`ForgetImage` and image steps to the node runtime |
| `cmd/virfieldd/main.go` | Wiring: node guest manager, node image builder |
| `docs/OPERATIONS.md`, `docs/VERIFICATION.md`, spec | Operator steps and live record |

Node-side layout (all under the node account, no sudo):

```
$HOME/virfield/leases/<vf-…>/     kind=vm  golden  port  state  storage/data.qcow2 (+ dockur identity files)
$HOME/virfield/images/<name>/     same layout, published on 127.0.0.1 only
<goldens_dir>/<name>.qcow2        read-only goldens (bootstrap and secured), 0444
<goldens_dir>/<name>.opencore.plist
```

Container name: `virfield-<vm>`. Port range: `port_base … port_base+max-1` for leases, `port_base+max … port_base+2*max-1` for image VMs.

---

## Part A — SSH endpoint = IP + port

### Task 1: Domain fields

**Files:**
- Modify: `internal/domain/model.go` (VM struct ~line 153, Lease struct ~line 107)
- Test: `internal/domain/model_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestLeaseAndVMCarrySSHPort(t *testing.T) {
	b, _ := json.Marshal(VM{Name: "vf-a", IP: "192.168.1.86", SSHPort: 22101})
	if !strings.Contains(string(b), `"ssh_port":22101`) {
		t.Fatal(string(b))
	}
	b, _ = json.Marshal(Lease{ID: "lease-a", IP: "192.168.1.86", SSHPort: 22101})
	if !strings.Contains(string(b), `"ssh_port":22101`) {
		t.Fatal(string(b))
	}
	b, _ = json.Marshal(VM{Name: "vf-b", IP: "10.0.0.2"})
	if strings.Contains(string(b), "ssh_port") {
		t.Fatal("port 0 must be omitted so existing leases are unchanged")
	}
}
```

- [ ] **Step 2: Run:** `go test ./internal/domain/ -run TestLeaseAndVMCarrySSHPort` — Expected: FAIL (unknown field `SSHPort`).
- [ ] **Step 3: Implement.** In `VM` after `IP`: `SSHPort int `+"`json:\"ssh_port,omitempty\"`"+` // 0 means 22 on IP`. In `Lease` after `IP`: the same field and comment.
- [ ] **Step 4: Run** the test — Expected: PASS. Run `go build ./...`.
- [ ] **Step 5: Commit** `git commit -am "Carry an SSH port with VM and lease addresses"`

### Task 2: Controller copies and compares the port

**Files:**
- Modify: `internal/control/controller.go` (~747 drift, ~904-915 readiness)
- Modify: `internal/control/tunnel.go` (~48, ~83)
- Test: `internal/control/controller_test.go` (use the existing fake runtime helpers in that file; search for `SSHAvailable: true` to find the fixture that reports a running VM)

- [ ] **Step 1: Write the failing tests** — two tests next to the existing readiness tests:
  1. `TestReadyLeaseTakesSSHPortFromRuntime`: fake runtime reports the lease VM `{State:"running", IP:"192.168.1.86", SSHPort:22101, SSHAvailable:true}`; after ticking to ready, `lease.SSHPort == 22101` and `lease.IP == "192.168.1.86"`.
  2. `TestReadyLeaseDriftsWhenSSHPortChanges`: lease ready with port 22101; runtime then reports port 22102 for two observations; lease becomes `needs_attention` with `vm_drift`.
- [ ] **Step 2: Run** `go test ./internal/control/ -run 'SSHPort'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Readiness: wherever `l.IP = v.IP` is assigned (both places), also `l.SSHPort = v.SSHPort`.
  - `ssh_configured` check: `if j.Phase == "ssh_configured" && (l.IP != v.IP || l.SSHPort != v.SSHPort || l.SSH == nil) {`.
  - Drift: `(l.SSH == nil || vm.IP != l.IP || vm.SSHPort != l.SSHPort)`; on drift also clear `l.SSHPort = 0`.
  - Cleanup where `l.IP = ""` is set: also `l.SSHPort = 0`.
  - `tunnel.go`: add `&& vm.SSHPort == l.SSHPort` to the identity checks at ~48 and ~83 (dial already uses `l.SSH.Port`).
- [ ] **Step 4: Run** `go test ./internal/control/` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Treat the SSH port as part of a lease's guest address"`

### Task 3: guestssh per-lease port, dialer, guest models

**Files:**
- Modify: `internal/guestssh/client.go` (Manager struct line 26; `connectCredentials` ~168-232)
- Modify: `internal/guestssh/lease.go` (lines 29, 48, 132)
- Test: `internal/guestssh/client_test.go`

- [ ] **Step 1: Write the failing tests.** Reuse the in-process SSH server helper already used by `client_test.go` (search for `ssh.NewServerConn` there). Add:

```go
func TestConnectUsesLeasePortAndDialer(t *testing.T) {
	addr := startGuest(t, "iMacPro1,1") // existing helper variant that answers hw.model
	_, port, _ := net.SplitHostPort(addr)
	var dialed string
	m := &Manager{Dir: t.TempDir(), Models: []string{"VirtualMac", "iMacPro1,1"}, Dial: func(ctx context.Context, l domain.Lease, address string) (net.Conn, error) {
		dialed = address
		return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+port)
	}}
	l := domain.Lease{ID: "image-test", GuestUser: "admin", IP: "192.168.1.86", SSHPort: 22101}
	g, err := m.Connect(context.Background(), l, l.IP, true)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	if dialed != "192.168.1.86:22101" {
		t.Fatal(dialed)
	}
}

func TestConnectRejectsUnlistedGuestModel(t *testing.T) {
	addr := startGuest(t, "iMacPro1,1")
	host, port, _ := net.SplitHostPort(addr)
	m := &Manager{Dir: t.TempDir(), SSHPort: port}
	_, err := m.Connect(context.Background(), domain.Lease{ID: "image-test", GuestUser: "admin"}, host, true)
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != "guest_identity_failed" {
		t.Fatal(err)
	}
}
```

  If `client_test.go` has no helper that sets the reported `hw.model`, add a `model string` parameter to the existing helper (default call sites pass `"VirtualMac"`).
- [ ] **Step 2: Run** `go test ./internal/guestssh/` — Expected: FAIL (unknown fields `Models`, `Dial`).
- [ ] **Step 3: Implement.**

```go
type Manager struct {
	Dir, Scope, SSHPort string
	// Dial reaches a guest SSH address; nil dials it directly. Node VMs
	// tunnel through the pinned node connection.
	Dial func(ctx context.Context, l domain.Lease, address string) (net.Conn, error)
	// Models lists accepted `hw.model` substrings; empty means Apple VirtualMac.
	Models []string
}

func (e *Manager) port(l domain.Lease) string {
	if l.SSHPort > 0 {
		return strconv.Itoa(l.SSHPort)
	}
	if e.SSHPort != "" {
		return e.SSHPort
	}
	return "22"
}

func (e *Manager) dial(ctx context.Context, l domain.Lease, address string) (net.Conn, error) {
	if e.Dial != nil {
		return e.Dial(ctx, l, address)
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, "tcp", address)
}

func (e *Manager) acceptedModel(model string) bool {
	models := e.Models
	if len(models) == 0 {
		models = []string{"VirtualMac"}
	}
	for _, m := range models {
		if strings.Contains(model, m) {
			return true
		}
	}
	return false
}
```

  In `connectCredentials` replace the port/dialer block with `address := net.JoinHostPort(ip, e.port(l))` and `raw, err := e.dial(ctx, l, address)`, use `address` in `NewClientConn`, and replace the model check with `if err != nil || !e.acceptedModel(model) {` (message: `"Refusing provisioning: target is not an accepted virtual Mac"`).
  In `lease.go` copy all fields when deriving managers: `source := &Manager{Dir: e.Dir, SSHPort: e.SSHPort, Dial: e.Dial, Models: e.Models}` and the same for `scoped` (plus `Scope: "leases"`); `image := domain.Lease{ID: l.ImageID, GuestUser: l.GuestUser, SSHPort: l.SSHPort}` so the image credential connects to the clone's port; return `Port: portNumber(l)` where

```go
func portNumber(l domain.Lease) int {
	if l.SSHPort > 0 {
		return l.SSHPort
	}
	return 22
}
```

- [ ] **Step 4: Run** `go test ./internal/guestssh/ ./internal/tartimage/` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Let guest SSH use a lease port, a routed dialer and listed guest models"`

### Task 4: Client ssh-config uses the lease port

**Files:**
- Modify: `internal/client/identity.go:68,96`
- Test: `internal/client/identity_test.go`

- [ ] **Step 1: Write the failing test** — copy the existing happy-path ssh-config test, set `lease.SSH.Port = 22101`, and assert the config contains `"  Port 22101\n"` and the known_hosts line is still `lease.ID + " " + hostKey`.
- [ ] **Step 2: Run** `go test ./internal/client/` — Expected: FAIL (`lease has no verified SSH connection`).
- [ ] **Step 3: Implement.** Condition: `l.SSH.Port < 1 || l.SSH.Port > 65535` instead of `!= 22`. Config: `Port %d` with `l.SSH.Port` inserted after `l.SSH.User` in the `Sprintf` arguments.
- [ ] **Step 4: Run** `go test ./internal/client/` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Write the lease's SSH port into client ssh-config"`

---

## Part B — Node VM leases

### Task 5: Config `nodes[].vms`

**Files:**
- Modify: `internal/config/config.go` (Node struct, `validateNodes`)
- Modify: `internal/baremetal/runtime.go` (Node struct)
- Modify: `cmd/virfieldd/main.go` (node mapping ~line 168)
- Test: `internal/config/config_test.go` (`TestRuntimeNodesValidate` table)

- [ ] **Step 1: Write the failing test** — extend the table in `TestRuntimeNodesValidate`:

```go
vms := `,"vms":{"image":"docker.io/dockurr/macos@sha256:08d1bcaac74ad44d548b7ce683b3dded083512d2a0d26416f9a620b763f8ea3d","goldens_dir":"/run/media/system/Data/virfield/goldens","port_base":22100,"max":2,"cpu":4,"memory_gb":8,"disk_size":"100G"}`
// cases:
"with vms":      strings.Replace(valid, `"arch":"amd64"`, `"arch":"amd64"`+vms, 1),
"vm image tag":  strings.Replace(valid, `"arch":"amd64"`, `"arch":"amd64"`+strings.Replace(vms, "@sha256:08d1bcaac74ad44d548b7ce683b3dded083512d2a0d26416f9a620b763f8ea3d", ":latest", 1), 1),
"vm low port":   strings.Replace(valid, `"arch":"amd64"`, `"arch":"amd64"`+strings.Replace(vms, "22100", "80", 1), 1),
"vm on macOS":   strings.Replace(strings.Replace(valid, `"os":"linux"`, `"os":"macOS"`, 1), `"arch":"amd64"`, `"arch":"amd64"`+vms, 1),
```

  `"with vms"` must load with `Nodes[0].VMs.Max == 2`; the other three must be rejected.
- [ ] **Step 2: Run** `go test ./internal/config/` — Expected: FAIL.
- [ ] **Step 3: Implement.**

```go
// NodeVMs runs disposable macOS VMs as rootless Podman containers on a Linux
// node. Goldens are read-only qcow2 files in GoldensDir; each lease publishes
// guest SSH on a port from PortBase.
type NodeVMs struct {
	Image      string `json:"image"`
	GoldensDir string `json:"goldens_dir"`
	PortBase   int    `json:"port_base"`
	Max        int    `json:"max"`
	CPU        int    `json:"cpu"`
	MemoryGB   int    `json:"memory_gb"`
	DiskSize   string `json:"disk_size"`
}
```

  Add `VMs *NodeVMs `+"`json:\"vms,omitempty\"`"+` to `Node`. In `validateNodes`, when `n.VMs != nil` require: `n.OS == "linux"`; `Image` matches `^[a-z0-9.-]+(/[a-z0-9._-]+)+@sha256:[0-9a-f]{64}$`; `GoldensDir` absolute without quotes/spaces; `1024 <= PortBase` and `PortBase+2*Max <= 65535`; `1 <= Max <= 8`; `CPU >= 1`; `MemoryGB >= 4`; `DiskSize` matches `^[1-9][0-9]*G$`. Error text: `"runtime node <id> vms: <what>"`.
  In `baremetal.Node` add `VMs *VMConfig` with the same fields (type `VMConfig` in `vm.go`, Task 7) and map it in `main.go`.
- [ ] **Step 4: Run** `go test ./internal/config/ && go build ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Configure macOS VMs on a Linux node"`

### Task 6: Node VM scripts

**Files:**
- Create: `internal/baremetal/vmscripts.go`
- Test: `internal/baremetal/vmscripts_test.go`

The scripts run as the node account with `sh -c <script> sh <args…>`; arguments are validated by Go before quoting. A fake `podman` on `PATH` records its argv in tests.

- [ ] **Step 1: Write the failing test** (local `/bin/sh`, private HOME, fake podman):

```go
func fakePodman(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "podman.log")
	// Records argv; `inspect` reports running only for containers listed in
	// $HOME/running; `run -d --name X` adds X; `stop|kill|rm -f X` removes it.
	script := `#!/bin/sh
echo "$@" >> ` + log + `
case "$1" in
  inspect) grep -qx "$4" "$HOME/running" 2>/dev/null && echo true || exit 125 ;;
  run) [ "$2" = -d ] && [ "$3" = --name ] && echo "$4" >> "$HOME/running" ;;
  stop|kill) grep -vx "$4" "$HOME/running" > "$HOME/running.t" 2>/dev/null; mv "$HOME/running.t" "$HOME/running" 2>/dev/null ;;
  rm) grep -vx "$3" "$HOME/running" > "$HOME/running.t" 2>/dev/null; mv "$HOME/running.t" "$HOME/running" 2>/dev/null ;;
esac
exit 0
`
	os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0755)
	return dir, log
}

func runScript(t *testing.T, home, path, script string, args ...string) (string, error) {
	cmd := exec.Command("/bin/sh", append([]string{"-c", script, "sh"}, args...)...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + path + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestVMCloneAllocatesAPortAndRecordsTheGolden(t *testing.T) {
	home, goldens := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(goldens, "sonoma-secure.qcow2"), nil, 0444)
	bin, log := fakePodman(t)
	image := "docker.io/dockurr/macos@sha256:" + strings.Repeat("a", 64)
	for _, vm := range []string{"vf-aaaaaaaaaaaaaaaa", "vf-bbbbbbbbbbbbbbbb"} {
		if out, err := runScript(t, home, bin, vmCloneScript, "leases", vm, "sonoma-secure", goldens, image, "22100", "2"); err != nil {
			t.Fatal(out, err)
		}
	}
	a, _ := os.ReadFile(filepath.Join(home, "virfield/leases/vf-aaaaaaaaaaaaaaaa/port"))
	b, _ := os.ReadFile(filepath.Join(home, "virfield/leases/vf-bbbbbbbbbbbbbbbb/port"))
	if string(a) != "22100" || string(b) != "22101" {
		t.Fatal(string(a), string(b))
	}
	if out, err := runScript(t, home, bin, vmCloneScript, "leases", "vf-cccccccccccccccc", "sonoma-secure", goldens, image, "22100", "2"); err == nil {
		t.Fatal("third clone admitted beyond max", out)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "qemu-img create -q -f qcow2 -b "+goldens+"/sonoma-secure.qcow2 -F qcow2 /out/data.qcow2") {
		t.Fatal(string(calls))
	}
}
```

  Add similar focused tests: `TestVMStartPublishesOnlyItsPort` (args `leases vm … publish=0.0.0.0` → podman run line contains `-p 0.0.0.0:22100:22`, `--name virfield-vf-…`, `-v <goldens>:<goldens>:ro,z`, `-v <goldens>/sonoma-secure.opencore.plist:/custom.plist:ro,z`, `--device /dev/kvm`, `-e USER_PORTS=22`; image VMs use `images` scope and `127.0.0.1`), `TestVMStopRemovesTheContainerKeepsTheDisk`, `TestVMDeleteRemovesEverything`, `TestVMProbeReportsGoldensAndVMs` (output lines `golden=sonoma-secure`, `vm=leases vf-… running 22100 sonoma-secure`), `TestVMCommitWritesAReadOnlyGolden` (podman qemu-img convert line, `.tmp` renamed to `<name>.qcow2`, plist copied from the source golden, mode 0444, image dir removed).
- [ ] **Step 2: Run** `go test ./internal/baremetal/ -run TestVM` — Expected: FAIL (undefined scripts).
- [ ] **Step 3: Implement** `vmscripts.go`:

```go
package baremetal

// Node VM scripts. Arguments: $1 scope (leases|images), $2 VM name, then
// script-specific values validated by Go. Every script touches only
// $HOME/virfield/<scope>/<vm>, the container virfield-<vm>, and (commit)
// one new file in the goldens directory.

const vmDirs = `set -eu
d="$HOME/virfield/$1/$2"
c="virfield-$2"
`

// vmCloneScript: $3 golden, $4 goldens dir, $5 image, $6 port base, $7 max.
const vmCloneScript = vmDirs + `g="$4/$3.qcow2"
[ -f "$g" ]
mkdir -p "$HOME/virfield/$1"
exec 9>"$HOME/virfield/$1/.ports.lock"
flock 9
if [ ! -f "$d/port" ]; then
  i=0
  while [ "$i" -lt "$7" ]; do
    p=$(( $6 + i ))
    grep -qxs "$p" "$HOME"/virfield/"$1"/*/port || break
    i=$(( i + 1 ))
  done
  [ "$i" -lt "$7" ] || { echo "no free VM port" >&2; exit 3; }
  umask 077
  mkdir -p "$d/storage"
  chattr +C "$d/storage" 2>/dev/null || true
  printf vm > "$d/kind"
  printf '%s' "$3" > "$d/golden"
  printf '%s' "$p" > "$d/port"
fi
[ -f "$d/storage/data.qcow2" ] || podman run --rm --entrypoint qemu-img -v "$4:$4:ro,z" -v "$d/storage:/out:Z" "$5" create -q -f qcow2 -b "$g" -F qcow2 /out/data.qcow2
printf stopped > "$d/state"
`

// vmStartScript: $3 goldens dir, $4 image, $5 publish address, $6 cpu,
// $7 memory GB, $8 disk size, $9 keep-awake file (may be empty).
const vmStartScript = vmDirs + `[ -f "$d/port" ]
g=$(cat "$d/golden")
p=$(cat "$d/port")
if ! podman inspect -f '{{.State.Running}}' "$c" 2>/dev/null | grep -qx true; then
  podman rm -f "$c" >/dev/null 2>&1 || true
  podman run -d --name "$c" --device /dev/kvm --device /dev/net/tun --cap-add NET_ADMIN \
    -e DISK_FMT=qcow2 -e "DISK_SIZE=$8" -e "RAM_SIZE=$7G" -e "CPU_CORES=$6" -e USER_PORTS=22 \
    -p "$5:$p:22" -v "$d/storage:/storage:Z" -v "$3:$3:ro,z" -v "$3/$g.opencore.plist:/custom.plist:ro,z" \
    --stop-timeout 30 "$4" >/dev/null
fi
if [ -n "${9:-}" ] && [ ! -e "$9" ]; then
  : > "$9"
  : > "$d/hold-created"
fi
printf running > "$d/state"
`

// vmStopScript: $3 keep-awake file. macOS clones can hang at the end of a
// guest shutdown, so stop is ACPI with a bounded wait, then kill.
const vmStopScript = vmDirs + `[ -d "$d" ] || exit 0
podman stop -t 30 "$c" >/dev/null 2>&1 || true
podman kill "$c" >/dev/null 2>&1 || true
podman rm -f "$c" >/dev/null 2>&1 || true
if [ -f "$d/hold-created" ]; then
  [ -n "${3:-}" ] && rm -f "$3"
  rm -f "$d/hold-created"
fi
printf stopped > "$d/state"
`

const vmDeleteScript = vmStopScript + `rm -rf "$d"
`

// vmCommitScript: $3 goldens dir, $4 image. Writes <goldens>/<vm>.qcow2 once.
const vmCommitScript = vmDirs + `[ -f "$d/storage/data.qcow2" ]
out="$3/$2.qcow2"
[ ! -e "$out" ]
podman run --rm --entrypoint qemu-img -v "$3:$3:z" -v "$d/storage:/src:ro,Z" "$4" convert -O qcow2 /src/data.qcow2 "$3/.$2.tmp"
mv "$3/.$2.tmp" "$out"
cp "$3/$(cat "$d/golden").opencore.plist" "$3/$2.opencore.plist"
chmod 444 "$out" "$3/$2.opencore.plist"
rm -rf "$d"
`

// vmProbeLines lists goldens and VMs; appended to probeScript when a node
// has VMs, with $1 the goldens dir.
const vmProbeLines = `for f in "$1"/*.qcow2; do
  [ -f "$f" ] && echo "golden=$(basename "$f" .qcow2)"
done
for s in leases images; do
  for d in "$HOME"/virfield/$s/*/; do
    [ -f "$d/kind" ] || continue
    n=$(basename "$d")
    st=stopped
    podman inspect -f '{{.State.Running}}' "virfield-$n" 2>/dev/null | grep -qx true && st=running
    echo "vm=$s $n $st $(cat "$d/port") $(cat "$d/golden")"
  done
done
`
```

  Note the host-lease `probeScript` must skip lease folders that have a `kind` file (they are VMs): add `[ -f "$d/kind" ] && continue` to its loop.
- [ ] **Step 4: Run** `go test ./internal/baremetal/` — Expected: PASS (existing host-lease tests unchanged).
- [ ] **Step 5: Commit** `git commit -am "Add node scripts for macOS VM clones in Podman"`

### Task 7: Runtime: observe, clone, start, stop, delete VMs

**Files:**
- Create: `internal/baremetal/vm.go`
- Modify: `internal/baremetal/runtime.go` (probe struct, `probe`, `parseProbe`, `Observe`, `host`, Clone/Start/Stop/Delete, `runLease`)
- Test: `internal/baremetal/vm_test.go`

- [ ] **Step 1: Write the failing tests** using `testRuntime` with an `htpc` node that has `VMs: &VMConfig{Image: …, GoldensDir: <tmp>, PortBase: 22100, Max: 2, CPU: 4, MemoryGB: 8, DiskSize: "100G"}` and the fake podman on the session `PATH` (extend `localSession` with a `path` field):
  1. `TestNodeVMGoldensAreObserved`: a `sonoma-secure.qcow2` in the goldens dir → observation has VM `{Name:"sonoma-secure", Location:"htpc", State:"stopped", OS:"macOS"}` beside the `bazzite` pseudo-golden; host `Capacity.MaxVMs == 3` (host lease + 2 VMs).
  2. `TestNodeVMLeaseLifecycle`: `Clone(Template{Name:"sonoma-secure"}, lease with ImageID "image-x")` → VM `vf-…` stopped; `Start` → VM running with `IP` = node IP, `SSHPort` = 22100, `SSHAvailable` true, `OS` "macOS"; `Stop` → stopped; `Delete` → absent.
  3. `TestNodeVMCloneRequiresAVerifiedImage`: `Clone` of a VM golden with empty `ImageID` returns `invalid_request` (bootstrap goldens are never leased directly).
  4. `TestHostLeaseStillWorksBesideVMs`: `Clone(Template{Name:"bazzite"})` keeps the R1 host-lease path (no `kind` file).
- [ ] **Step 2: Run** `go test ./internal/baremetal/ -run 'NodeVM|HostLeaseStill'` — Expected: FAIL.
- [ ] **Step 3: Implement** `vm.go`:

```go
package baremetal

// VMConfig mirrors config.NodeVMs.
type VMConfig struct {
	Image, GoldensDir, DiskSize string
	PortBase, Max, CPU, MemoryGB int
}

// vmRecord is one VM folder reported by the probe.
type vmRecord struct {
	scope, state, golden string
	port                 int
}

func (r *Runtime) isVMGolden(n Node, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return n.VMs != nil && r.last[n.ID].goldens[name]
}

func (r *Runtime) vmArgs(n Node, scope, name string, script string, extra ...string) string {
	return command(script, append([]string{scope, name}, extra...)...)
}

func (r *Runtime) cloneVM(ctx context.Context, n Node, t domain.Template, l domain.Lease) error {
	if l.ImageID == "" {
		return domain.Err("invalid_request", "Node VM leases require a verified image")
	}
	v := n.VMs
	return r.runOn(ctx, n, r.vmArgs(n, "leases", l.VMName, vmCloneScript, t.Name, v.GoldensDir, v.Image, strconv.Itoa(v.PortBase), strconv.Itoa(v.Max)))
}

func (r *Runtime) startVM(ctx context.Context, n Node, scope, name, publish string) error {
	v := n.VMs
	return r.runOn(ctx, n, r.vmArgs(n, scope, name, vmStartScript, v.GoldensDir, v.Image, publish, strconv.Itoa(v.CPU), strconv.Itoa(v.MemoryGB), v.DiskSize, n.KeepAwakeFile))
}
```

  `runOn(ctx, n, cmd string) error` is `runLease` without the name check (dial, `reached`, `Run`). In `runtime.go`:
  - `probe` gets `goldens map[string]bool` and `vms map[string]vmRecord`; `parseProbe` reads `golden=` (valid names) and `vm=<scope> <name> <state> <port> <golden>` (scope leases|images, valid name, state running|stopped, port in the node range).
  - `probe()` runs `command(probeScript+vmProbeLines, n.VMs.GoldensDir)` when `n.VMs != nil` (and `probeScript` alone otherwise).
  - `Observe` appends golden VMs (`State:"stopped"`, `OS:"macOS"`, `Provider`, `HostID`, `ContainerID: n.ID`, `Location: n.ID`) and VM records (`IP` and `SSHPort` set and `SSHAvailable` when running and reachable; running ones count in `UsedVMs`). Image-scope VMs are named by their folder (the image template name). Persisted probes (`persist.go` `remembered`) gain `Goldens []string` and `VMs map[string]…` so asleep admission keeps working.
  - `host()`: `MaxVMs: 1 + n.VMs.Max` when VMs are configured.
  - `Clone`: after `ensureAwake`, `if r.isVMGolden(n, t.Name) { return r.cloneVM(ctx, n, t, l) }`.
  - `Start/Stop/Delete`: if the last probe has a VM record for `l.VMName` in scope `leases`, run the VM script (`startVM(…, "leases", l.VMName, "0.0.0.0")`, `vmStopScript`/`vmDeleteScript` with `n.KeepAwakeFile`); otherwise the host-lease scripts. A VM clone marks the node changed (`r.reached`), so the controller's next observation re-probes before it starts the lease and sees the VM record.
  - Guard against a stale probe sending a VM folder to a host script: in `startScript`, `stopScript` and `deleteScript` add `[ -f "$d/kind" ] && { echo "node VM folder" >&2; exit 3; }` right after the folder-exists check, so a mis-dispatch fails loudly instead of orphaning a container. Test: `TestHostScriptsRefuseVMFolders`.
- [ ] **Step 4: Run** `go test ./internal/baremetal/` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Observe and run macOS VM clones on Linux nodes"`

### Task 8: Guest SSH through the node; lease preparation for VM leases

**Files:**
- Modify: `internal/baremetal/ssh.go` (session `Dial`, `Prepare` delegation)
- Modify: `internal/baremetal/vm.go` (`DialGuest`, `SetGuests`)
- Modify: `internal/fleet/fleet.go:151-190` (route `Forget`/`ForgetImage`)
- Test: `internal/baremetal/vm_test.go`, `internal/fleet/fleet_test.go`

- [ ] **Step 1: Write the failing tests.**
  1. `TestDialGuestTunnelsToNodeLoopbackPort`: in `ssh_test.go`'s in-process server add `direct-tcpip` handling that records the requested host/port and pipes to a local echo listener; `r.DialGuest(ctx, domain.Lease{Location:"htpc", SSHPort:22100}, "192.168.1.86:22100")` dials `127.0.0.1:22100` on the node.
  2. `TestPrepareDelegatesVMLeasesToGuests`: a runtime whose last probe has VM `vf-x` → `Prepare(lease vf-x)` calls the injected guests preparer (fake) and returns its connection; a host lease still uses the authorized_keys path.
  3. `TestFleetForgetsRoutedLeases`: `fleet.NewPreparer(primary, {"baremetal": node})` → `Forget(l with Provider baremetal)` reaches `node.Forget`.
- [ ] **Step 2: Run** `go test ./internal/baremetal/ ./internal/fleet/` — Expected: FAIL.
- [ ] **Step 3: Implement.**

```go
// session gains: Dial(network, address string) (net.Conn, error)
func (s *sshSession) Dial(network, address string) (net.Conn, error) { return s.client.Dial(network, address) }

// DialGuest reaches a node VM's published SSH port from the node itself, so
// Virfield's guest SSH never depends on the port being published on the LAN.
func (r *Runtime) DialGuest(ctx context.Context, l domain.Lease, _ string) (net.Conn, error) {
	n, err := r.node(l.Location)
	if err != nil {
		return nil, err
	}
	if l.SSHPort < 1 {
		return nil, domain.Err("guest_connect_failed", "Node VM lease has no SSH port")
	}
	s, err := r.dial(ctx, n)
	if err != nil {
		return nil, err
	}
	conn, err := s.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.SSHPort)))
	if err != nil {
		s.Close()
		return nil, err
	}
	return &tunnelConn{Conn: conn, session: s}, nil
}

// tunnelConn closes the node session with the forwarded connection.
type tunnelConn struct {
	net.Conn
	session session
}

func (c *tunnelConn) Close() error { err := c.Conn.Close(); c.session.Close(); return err }

// Guests prepares node VM leases with the image pipeline's credentials.
type Guests interface {
	Prepare(context.Context, domain.Lease) (domain.SSHConnection, error)
	Forget(domain.Lease) error
}

func (r *Runtime) SetGuests(g Guests) { r.guests = g }
```

  `Prepare`: if the last probe of `l.Location` has a VM record for `l.VMName` → `return r.guests.Prepare(ctx, l)` (error `ssh_profile_missing` if `r.guests == nil`). Add `Forget(l)`: VM lease → `r.guests.Forget(l)`, else nil. In `fleet.go`, make `Forget` and `ForgetImage` forward to the routed preparer when it implements `interface{ Forget(domain.Lease) error }` / `ForgetImage`, else keep current behavior. The `localSession` test double implements `Dial` with `net.Dial`.
- [ ] **Step 4: Run** `go test ./internal/baremetal/ ./internal/fleet/` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Prepare node VM leases over SSH tunnelled through the node"`

---

## Part C — Securing node goldens

### Task 9: `NodeSource` image profile and fingerprint

**Files:**
- Modify: `internal/domain/image.go`, `internal/control/image_identity.go`, `internal/control/controller.go:95-105` (config defaults for node images)
- Test: `internal/domain/image_test.go`, `internal/control/image_identity_test.go`

- [ ] **Step 1: Write the failing tests.**

```go
func TestNodeSourceProfile(t *testing.T) {
	p := ImageProfile{Node: &NodeSource{Golden: "sonoma-14.8.9-23J631-20261008"}, MacOS: "14.8.9", Build: "23J631", DisableSIP: true}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := p
	bad.Node = &NodeSource{Golden: "../etc"}
	if bad.Validate() == nil {
		t.Fatal("accepted a path as golden")
	}
	mixed := p
	mixed.Tart = &TartSource{Upstream: "ghcr.io/a/b@sha256:" + strings.Repeat("0", 64)}
	if mixed.Validate() == nil {
		t.Fatal("accepted two sources")
	}
	withXcode := p
	withXcode.Provision = "uitest-27-v1"
	if withXcode.Validate() == nil {
		t.Fatal("node images carry no provisioning in R2")
	}
}
```

  In `image_identity_test.go`: the fingerprint of a node profile differs from the same profile with another golden, and the existing golden-Lume fingerprint test value `cb5a8a97…add2426` is unchanged.
- [ ] **Step 2: Run** `go test ./internal/domain/ ./internal/control/ -run 'NodeSource|Fingerprint'` — Expected: FAIL.
- [ ] **Step 3: Implement.** `type NodeSource struct { Golden string `+"`json:\"golden\"`"+` }`; `ImageProfile.Node *NodeSource `+"`json:\"node,omitempty\"`"+`. Validate: `Golden` is `domain.ValidName`; `Node` excludes `Tart`, `Registry`, `URL`, `SHA256`, `Size`, `Provision`, `Xcode`; requires valid `MacOS` and `Build`. Fingerprint: add `Node *struct{ Golden string } `+"`json:\"node,omitempty\"`"+` to `identity` (omitempty keeps old fingerprints). Config defaults: node images default `GuestUser` to `admin` and reject others, like Tart.
- [ ] **Step 4: Run** `go test ./internal/domain/ ./internal/control/` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Describe node goldens as an image source"`

### Task 10: Controller node image chain

**Files:**
- Modify: `internal/control/image_build.go` (~39-160, 250)
- Test: `internal/control/image_build_test.go`

- [ ] **Step 1: Write the failing tests** with the existing fake `ImageBuilder` (records steps):
  1. `TestNodeImageRunsBuildQualifyStopCommit`: a template `{ID:"sonoma-14-secure", Name:"sonoma-14-secure", Location:"htpc", Provider:"baremetal", Image: node profile}`; `BuildImage` → steps `build, qualify, stop, commit` in order; after `commit_done` the fake runtime reports golden VM `htpc/sonoma-14-secure` stopped → lease `image_ready` with `ImageManifest == imageFingerprint(profile)`.
  2. `TestNodeImageBuildUsesNodeCapacity`: two running Tart leases filling Mac capacity do not block a node image build.
  3. `TestNodeImageRecovery`: `qualify_dispatched` retries from `build_done`; `commit_dispatched` is not retryable.
- [ ] **Step 2: Run** `go test ./internal/control/ -run NodeImage` — Expected: FAIL.
- [ ] **Step 3: Implement.**

```go
var nodeImageNext = map[string]string{"queued": "build", "build_done": "qualify", "qualify_done": "stop", "stop_done": "commit"}

func imageChain(p *domain.ImageProfile) (map[string]string, string) {
	switch {
	case p.Node != nil:
		return nodeImageNext, "commit_done"
	case p.Tart != nil:
		return tartImageNext, "stop_done"
	}
	return imageNext, "stop_done"
}
```

  Use `imageChain` in `advanceImageBuild` for both the next step and the `image_ready` condition (`j.Phase == final`). In `buildImageLocked`, use `c.capacityOn(ls, t.Location)` when `c.secondaryProvider(t.Provider)`, and set the image lease `Provider: t.Provider`. Retry map: add `"commit_dispatched"` as non-retryable (absent) and keep `qualify_dispatched→build_done`.
- [ ] **Step 4: Run** `go test ./internal/control/` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Build node images as clone, secure, stop and commit"`

### Task 11: Node image builder

**Files:**
- Create: `internal/nodeimage/builder.go`, `internal/nodeimage/builder_test.go`
- Modify: `internal/baremetal/vm.go` (`CloneImage`, `StartImage`, `StopImage`, `CommitImage`)
- Modify: `internal/fleet/fleet.go` (expose the node runtime's image operations to the builder)

- [ ] **Step 1: Write the failing test** with fakes for the runtime and guests:

```go
func TestQualifySecuresRebootsAndVerifies(t *testing.T) {
	rt := &fakeRuntime{ip: "192.168.1.86", port: 22164}
	g := &fakeGuests{os: "14.8.9\n23J631", sip: "System Integrity Protection status: disabled."}
	b := New(Config{StateDir: t.TempDir(), Runtime: rt, Guests: g})
	l := domain.Lease{ID: "image-a", VMName: "sonoma-14-secure", Location: "htpc", GuestUser: "admin"}
	p := domain.ImageProfile{Node: &domain.NodeSource{Golden: "sonoma-14.8.9-23J631-20261008"}, MacOS: "14.8.9", Build: "23J631", DisableSIP: true}
	for _, step := range []string{"build", "qualify", "stop", "commit"} {
		if err := b.Step(context.Background(), l, p, step, func(string) error { return nil }); err != nil {
			t.Fatal(step, err)
		}
	}
	want := []string{"clone:sonoma-14.8.9-23J631-20261008", "start", "connect:bootstrap", "secure", "shutdown", "start", "connect:secured", "shutdown", "stop", "commit"}
	if strings.Join(rt.calls, ",") != strings.Join(want, ",") {
		t.Fatal(rt.calls)
	}
}
```

  Plus `TestQualifyHaltsOnWrongSIP` (verify fails → `Stop` called, error `sip_verification_failed`).
- [ ] **Step 2: Run** `go test ./internal/nodeimage/` — Expected: FAIL.
- [ ] **Step 3: Implement** (≈150 lines):
  - `Runtime` interface: `Observe`, `CloneImage(ctx, l, golden string) error`, `StartImage(ctx, l) error`, `StopImage(ctx, l) error`, `CommitImage(ctx, l) error`.
  - `Guests` interface as in `tartimage/qualify.go` (`Connect`, `Secure`, `Password`), real one wraps a `guestssh.Manager{Dir: StateDir, Dial: node.DialGuest, Models: []string{"iMacPro1,1"}}`; the image lease passed to guestssh carries `SSHPort` from the observed image VM.
  - `Step`: `build` → `CloneImage(l, p.Node.Golden)`; `qualify` → boot (`StartImage`, wait for the image VM `running` with `SSHPort`), bootstrap connect, `Secure`, guest `sudo -n /sbin/shutdown -h now` then `StopImage` (clones hang at the end of shutdown), boot, secured connect, `qualify.OS`, `qualify.SIP`, `qualify.Desktop`, `qualify.Password`, write `verification.json` (`golden`, `macos`, `build`, `sip`), shutdown + `StopImage`; any error → `StopImage`. `stop` → `StopImage` and wait for stopped. `commit` → `CommitImage`.
  - `baremetal`: `CloneImage` runs `vmCloneScript` with scope `images`, name `l.VMName`, port base `PortBase+Max`; `StartImage` runs `vmStartScript` with scope `images` and publish `127.0.0.1`; `StopImage` runs `vmStopScript` scope `images`; `CommitImage` runs `vmCommitScript`. Image names must be `domain.ValidName` and must not start with `vf-`.
- [ ] **Step 4: Run** `go test ./internal/nodeimage/ ./internal/baremetal/` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Secure and commit node goldens with the guest SSH pipeline"`

### Task 12: Wiring

**Files:**
- Modify: `cmd/virfieldd/main.go` (~168-175 nodes, ~227-232 preparer, ~276-288 image builder)
- Modify: `internal/fleet/fleet.go` (image builder routing by provider)

- [ ] **Step 1: Write the failing test** `TestFleetRoutesImageStepsByProvider` in `fleet_test.go`: `fleet.NewImageBuilder(primary, map[string]ImageBuilder{"baremetal": node})` sends a lease with `Provider:"baremetal"` to `node` and others to `primary`.
- [ ] **Step 2: Run** `go test ./internal/fleet/` — Expected: FAIL.
- [ ] **Step 3: Implement** `NewImageBuilder` (same shape as `NewPreparer`). In `main.go`, when any node has `VMs`:

```go
guests := &guestssh.Manager{Dir: cfg.StateDir, Dial: nodes.DialGuest, Models: []string{"iMacPro1,1"}}
nodes.SetGuests(guests)
nodeImages := nodeimage.New(nodeimage.Config{StateDir: cfg.StateDir, Runtime: nodes, Guests: nodeimage.SSHGuests(guests)})
builder = fleet.NewImageBuilder(builder, map[string]fleet.ImageBuilder{nodes.ProviderName(): nodeImages})
```

  (`builder` is the Tart builder when present; `SetImageBuilder` is then called once with the routed builder.)
- [ ] **Step 4: Run** `make check` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "Wire node VM leases and node image builds into virfieldd"`

---

## Part D — Live acceptance and docs

### Task 13: Configure, deploy, build the secured goldens

- [ ] **Step 1:** Add to `~/.virfield-v2/config.tart.json` (and copy to `config.json`) under node `htpc`:

```json
"vms": {"image": "docker.io/dockurr/macos@sha256:08d1bcaac74ad44d548b7ce683b3dded083512d2a0d26416f9a620b763f8ea3d",
        "goldens_dir": "/run/media/system/Data/virfield/goldens",
        "port_base": 22100, "max": 2, "cpu": 4, "memory_gb": 8, "disk_size": "100G"}
```

  and templates:

```json
{"id": "htpc-sonoma-14", "name": "htpc-sonoma-14", "location": "htpc", "provider": "baremetal", "os": "macOS", "arch": "amd64", "guest_user": "admin",
 "image": {"node": {"golden": "sonoma-14.8.9-23J631-20261008"}, "macos": "14.8.9", "build": "23J631", "disable_sip": true}},
{"id": "htpc-sequoia-15", "name": "htpc-sequoia-15", "location": "htpc", "provider": "baremetal", "os": "macOS", "arch": "amd64", "guest_user": "admin",
 "image": {"node": {"golden": "sequoia-15.8.1-24H32-20261008"}, "macos": "15.8.1", "build": "24H32", "disable_sip": true}}
```

- [ ] **Step 2:** `make build`, install `bin/virfieldd` to `~/.virfield-v2/bin/`, restart (see OPERATIONS.md "Deploy"); confirm `/api/v1/status` lists goldens `sonoma-14.8.9-23J631-20261008` and `sequoia-15.8.1-24H32-20261008` on `htpc`.
- [ ] **Step 3:** `POST /api/v1/images/htpc-sonoma-14/build` and `…/htpc-sequoia-15/build`; poll jobs until both leases are `image_ready`. Expected node files: `goldens/htpc-sonoma-14.qcow2` and `goldens/htpc-sequoia-15.qcow2`, mode 0444.

### Task 14: Two-lease acceptance beside the host lease

- [ ] **Step 1:** Write `scratchpad/accept-r2.py` modeled on `accept-nodes.py`: acquire `htpc-sonoma-14`, `htpc-sequoia-15` and `htpc-bazzite` concurrently with fresh keys; wait ready; for each VM lease assert `ssh.port` in 22100–22101, `ssh -p <port>` with the lease key and pinned host key runs `sw_vers -productVersion` (14.8.9 / 15.8.1) and `csrutil status` (disabled), password auth is refused, the other lease's key is refused; `virfield ssh-config` output contains `Port <port>`; release all; ports closed, `~/virfield/leases/*` empty, no `virfield-vf-*` containers.
- [ ] **Step 2:** Run it; fix any defect test-first; record results.
- [ ] **Step 3:** Re-run the wake-by-lease test (`accept-wake.py`) with no open tunnels to the HTPC.

### Task 15: Docs

- [ ] Update `docs/OPERATIONS.md` (node VMs: config, goldens, image build, ports, stop semantics), `docs/VERIFICATION.md` (R2 live record), `docs/VIRFIELD-3-PLAN.md` status row, the R2 spec, and the Obsidian note; commit `git commit -am "Record remote nodes R2 live acceptance"`.

---

## Self-review notes

- Spec coverage: runtime/podman (Tasks 6–7), SIP via OpenCore plist (golden plist copied on commit, mounted at start), storage overlays + `chattr +C` (Task 6), securing (Tasks 9–11), per-lease port (Tasks 1–4, 8), capacity (Task 7 `MaxVMs`, Task 10 node capacity), stop semantics (Task 6 `vmStopScript`), live acceptance (Task 14). Xcode on node goldens is out of scope for R2 (spec: "when wanted").
- Security: bootstrap goldens are never leased (Task 7 requires `ImageID`); image VMs publish on node loopback only (Task 11); leases publish on the LAN only after cloning a secured golden whose SSH is key-only (guestssh `SecureImage`).
- Known follow-ups: VS Code remote debug and `DeviceSupport` need guest NFS reachability and do not work through a port forward; not in R2.
