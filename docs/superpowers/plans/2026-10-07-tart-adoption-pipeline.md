> **Reconciled status — 2026-10-14:** This is a dated design/execution record.
> It is not a current deployment runbook or live task tracker. See
> [current delivery state](../../VIRFIELD-3-PLAN.md) and
> [current acceptance](../../VERIFICATION.md); old paths/statuses and unchecked
> example steps below describe their original execution context.
> **2026-10-15 decision:** automatic VM account-password rotation is rejected;
> standard `admin/admin` is the approved target, still pending implementation.
> Historical rotation steps below are superseded by the [maintained plan](../../VIRFIELD-3-PLAN.md#decisions-and-current-boundary).

# Tart Adoption Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build qualified Tart goldens from digest-pinned Cirrus images (Ventura 13.7.4 + Xcode 15.2, Monterey 12.7.6 + Xcode 13.4.1) through the existing durable image job.

**Architecture:** Stage 2 of [Tart image adoption](../specs/2026-10-07-tart-image-adoption-design.md). A Tart `ImageProfile` source selects a Tart phase map in the controller (`resolve → build → qualify → stop`). `internal/tartimage` implements `control.ImageBuilder`: an embedded Packer recipe (Cirrus `tart-cli` plugin) builds the VM, and `internal/qualify` holds runtime-agnostic guest checks extracted from `internal/images`. Lume keeps working through the same extracted functions with user `lume`.

**Tech Stack:** Go, Packer 1.15.2 + `github.com/cirruslabs/tart` plugin 1.21.0, Tart 2.40.1, bash guest scripts.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/domain/image.go` | `TartSource` and profile validation |
| `internal/control/image_identity.go` | fingerprint includes the Tart upstream |
| `internal/config/config.go` | `runtime.tart.image` (Packer binary, Xcode archive dir) |
| `internal/qualify/qualify.go` | `Guest` interface, `Quote`, `pause` |
| `internal/qualify/disk.go` | APFS disk policy probe/parse/validate |
| `internal/qualify/desktop.go` | desktop probe and settled-Finder wait |
| `internal/qualify/guest.go` | OS version/build, SIP, password persistence |
| `internal/qualify/xcode.go` | Xcode bundle, signature, version and toolchain probe |
| `internal/qualify/devicesupport.go` | host DeviceSupport from the guest dyld export (moved verbatim) |
| `internal/tartimage/recipe/…` | embedded Packer template and guest scripts |
| `internal/tartimage/recipe.go` | recipe digest and materialization |
| `internal/tartimage/builder.go` | `Step`: resolve, build, stop; process runner |
| `internal/tartimage/qualify.go` | `Step`: qualify (boot, secure, reboot, verify, evidence) |
| `internal/control/image_build.go` | Tart phase map, progress text, recovery |
| `cmd/virfieldd/main.go` | wire the Tart image builder |
| `docs/OPERATIONS.md` | Tart image configuration and window procedure |

---

### Task 1: Tart image source in the domain

**Files:**
- Modify: `internal/domain/image.go`, `internal/control/image_identity.go`
- Test: `internal/domain/image_test.go`, `internal/control/image_identity_test.go`

- [x] **Step 1: Write the failing tests**

Append to `internal/domain/image_test.go`:

```go
func tartProfile() ImageProfile {
	return ImageProfile{
		Tart:       &TartSource{Upstream: "ghcr.io/cirruslabs/macos-ventura-base@sha256:9fb387bb987cdda942fbc27118296ed8e16a3359a672c95c6d5ac960165ef1c5"},
		MacOS:      "13.7.4",
		Build:      "22H420",
		Security:   "sip-disabled",
		DisableSIP: true,
		Provision:  "developer-v1",
		Xcode:      &XcodeRelease{Version: "15.2", Build: "15C500b", Requires: "13.5", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_15.2/Xcode_15.2.xip", SHA1: "3f1e6943264ba640f83e655768df439902acc406"},
	}
}

func TestTartProfileValidates(t *testing.T) {
	if err := tartProfile().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTartProfileRejectsUnpinnedOrCompetingSources(t *testing.T) {
	cases := map[string]func(*ImageProfile){
		"tag":        func(p *ImageProfile) { p.Tart.Upstream = "ghcr.io/cirruslabs/macos-ventura-base:latest" },
		"host":       func(p *ImageProfile) { p.Tart.Upstream = "docker.io/cirruslabs/macos-ventura-base@sha256:" + strings.Repeat("a", 64) },
		"short":      func(p *ImageProfile) { p.Tart.Upstream = "ghcr.io/cirruslabs/x@sha256:abc" },
		"ipsw":       func(p *ImageProfile) { p.URL = "https://updates.cdn-apple.com/x.ipsw" },
		"no build":   func(p *ImageProfile) { p.Build = "" },
		"no xcode":   func(p *ImageProfile) { p.Xcode = nil; p.Provision = "" },
		"recipe":     func(p *ImageProfile) { p.Provision = "security-v1"; p.Security = "automation"; p.Xcode = nil },
		"registry":   func(p *ImageProfile) { p.Registry = &RegistryReference{} },
	}
	for name, mutate := range cases {
		p := tartProfile()
		mutate(&p)
		if err := p.Validate(); err == nil {
			t.Fatalf("%s: accepted invalid Tart profile", name)
		}
	}
}
```

Create `internal/control/image_identity_test.go`:

```go
package control

import (
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

// The v2 Lume manifest identity must not change when Tart fields are added.
func TestLumeFingerprintIsStable(t *testing.T) {
	p := domain.ImageProfile{MacOS: "13.6", Build: "22G120", Security: "sip-disabled", Provision: "developer-v1", DisableSIP: true, URL: "https://updates.cdn-apple.com/x.ipsw", SHA256: "9bf095739b8b2d5ebd20f7e8de938f10bc449f9843de21c4a41ae54d73526728", Size: 12893555341, Xcode: &domain.XcodeRelease{Version: "15.2", Build: "15C500b", Requires: "13.5", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_15.2/Xcode_15.2.xip", SHA1: "3f1e6943264ba640f83e655768df439902acc406"}}
	if got := imageFingerprint(&p); got != "cb5a8a977aaceed301cc350945d4ba736c7c4aab3ea0124f764e70183add2426" {
		t.Fatalf("Lume fingerprint changed: %s", got)
	}
}

func TestTartUpstreamIsPartOfTheFingerprint(t *testing.T) {
	a := domain.ImageProfile{MacOS: "13.7.4", Build: "22H420", Tart: &domain.TartSource{Upstream: "ghcr.io/a/b@sha256:1"}}
	b := a
	b.Tart = &domain.TartSource{Upstream: "ghcr.io/a/b@sha256:2"}
	if imageFingerprint(&a) == imageFingerprint(&b) {
		t.Fatal("different upstream digests share an image identity")
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/domain/ ./internal/control/ -run 'Tart|LumeFingerprint' 2>&1 | tail -5`
Expected: FAIL to compile with `undefined: TartSource` / `unknown field Tart`.

- [x] **Step 3: Implement the domain type**

In `internal/domain/image.go` add `"regexp"` to the imports, add the field to `ImageProfile` (first field):

```go
	Tart       *TartSource        `json:"tart,omitempty"`
```

and add after the struct:

```go
// TartSource pins one upstream Tart OCI image. Only digest references on GHCR
// are accepted; a tag can move under a qualified golden.
type TartSource struct {
	Upstream string `json:"upstream"`
}

var tartUpstreamPattern = regexp.MustCompile(`^ghcr\.io/[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*@sha256:[0-9a-f]{64}$`)

func (s TartSource) Validate() error {
	if !tartUpstreamPattern.MatchString(s.Upstream) {
		return Err("invalid_profile", "Tart upstream must be a ghcr.io image pinned by sha256 digest")
	}
	return nil
}
```

In `Validate`, insert immediately before `if p.Registry != nil {`:

```go
	if p.Tart != nil {
		if p.Registry != nil || p.URL != "" || p.SHA256 != "" || p.Size != 0 || !ValidVersion(p.MacOS) || !ValidName(p.Build) || p.Provision != "developer-v1" {
			return Err("invalid_profile", "Tart images need expected macOS/build, developer-v1 and no competing source")
		}
		return p.Tart.Validate()
	}
```

- [x] **Step 4: Implement the fingerprint**

In `internal/control/image_identity.go` add inside `imageFingerprint`, after `type xcode struct {…}`:

```go
	type tart struct {
		Upstream string `json:"upstream"`
	}
```

add the field as the last member of `identity`:

```go
		Tart       *tart     `json:"tart,omitempty"`
```

and before `return fingerprint(v)`:

```go
	if p.Tart != nil {
		v.Tart = &tart{p.Tart.Upstream}
	}
```

- [x] **Step 5: Run the tests**

Run: `go test ./internal/domain/ ./internal/control/ 2>&1 | tail -3`
Expected: both `ok`.

- [x] **Step 6: Commit**

```bash
git add internal/domain/image.go internal/domain/image_test.go internal/control/image_identity.go internal/control/image_identity_test.go
git commit -m "Add digest-pinned Tart image source"
```

---

### Task 2: Tart image configuration

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [x] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestTartImageToolsRequireAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	base := `{"listen":"127.0.0.1:7780","state_dir":"/tmp/a","token_file":"/tmp/t","max_vms":2,"runtime":{"provider":"tart","tart":{"binary":"/opt/homebrew/bin/tart","host_id":"macbook","containers":[{"id":"samsung","path":"/Volumes/S/tart","kind":"external"}],"image":%s}},"templates":[]}`
	for name, image := range map[string]string{
		"valid":            `{"packer":"/opt/homebrew/bin/packer","xcode_archives":"/Users/a/cache/xcode"}`,
		"relative packer":  `{"packer":"packer","xcode_archives":"/Users/a/cache/xcode"}`,
		"missing archives": `{"packer":"/opt/homebrew/bin/packer"}`,
	} {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(fmt.Sprintf(base, image)), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if name == "valid" {
			if err != nil || cfg.Runtime.Tart.Image.Packer != "/opt/homebrew/bin/packer" {
				t.Fatal(cfg, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
```

(Add `"fmt"` to the imports if missing. Check the loader's name with `grep -n "^func Load" internal/config/config.go`; if it differs, use that name.)

- [x] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestTartImageToolsRequireAbsolutePaths -v 2>&1 | tail -5`
Expected: FAIL to compile — `cfg.Runtime.Tart.Image undefined`.

- [x] **Step 3: Implement**

In `internal/config/config.go`, add to `type Tart struct` (last field):

```go
	Image               *TartImage               `json:"image,omitempty"`
```

add the type after `TartRegistryCredential`:

```go
// TartImage enables Tart image adoption. Paths are operator-owned host paths.
type TartImage struct {
	Packer        string `json:"packer"`
	XcodeArchives string `json:"xcode_archives"`
}
```

and in `validateRuntime`, right after the registry-credential loop:

```go
		if t.Image != nil && (!filepath.IsAbs(t.Image.Packer) || !filepath.IsAbs(t.Image.XcodeArchives)) {
			return errors.New("tart image requires absolute packer and xcode_archives paths")
		}
```

- [x] **Step 4: Run the config tests**

Run: `go test ./internal/config/ 2>&1 | tail -3`
Expected: `ok`.

- [x] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "Configure Tart image adoption tools"
```

---

### Task 3: `qualify` package with disk policy and desktop wait

**Files:**
- Create: `internal/qualify/qualify.go`, `internal/qualify/disk.go`, `internal/qualify/desktop.go`
- Move tests: `internal/images/disk_policy_test.go` → `internal/qualify/disk_test.go`, `internal/images/desktop_test.go` → `internal/qualify/desktop_test.go`
- Modify: `internal/images/disk_policy.go`, `internal/images/engine.go`

- [x] **Step 1: Move the tests (they become the failing tests)**

```bash
git mv internal/images/disk_policy_test.go internal/qualify/disk_test.go
git mv internal/images/desktop_test.go internal/qualify/desktop_test.go
sed -i '' 's/^package images$/package qualify/' internal/qualify/disk_test.go internal/qualify/desktop_test.go
sed -i '' 's/parseDiskEvidence(/ParseDiskEvidence(/; s/disk\.validate(/disk.Validate(/; s/legacyUUID/allowAtRest/g' internal/qualify/disk_test.go
sed -i '' 's/waitDesktop(/WaitDesktop(/g' internal/qualify/desktop_test.go
```

- [x] **Step 2: Run them to verify they fail**

Run: `go test ./internal/qualify/ 2>&1 | tail -3`
Expected: FAIL — `undefined: ParseDiskEvidence`, `undefined: WaitDesktop`.

- [x] **Step 3: Create `internal/qualify/qualify.go`**

```go
// Package qualify holds runtime-agnostic guest qualification checks. Every
// check runs a fixed command over an authenticated, host-key-pinned session.
package qualify

import (
	"context"
	"io"
	"strings"
	"time"
)

// Guest is the authenticated SSH session a check runs on.
type Guest interface {
	Run(ctx context.Context, command, input string) (string, error)
	RunReader(ctx context.Context, timeout time.Duration, command string, input io.Reader) (string, error)
}

// Quote returns a single-quoted shell word.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
```

- [x] **Step 4: Create `internal/qualify/disk.go`**

Move `diskPolicyProbe`, `diskPolicyEvidence`, `diskEvidence`, `parseDiskEvidence` and `validate` from `internal/images/disk_policy.go` verbatim, exporting the names `DiskPolicyEvidence`, `DiskEvidence`, `ParseDiskEvidence`, `Validate` and renaming the `validate` parameter to `allowEncryptionAtRest`. Then add:

```go
// DiskPolicy reads both boot volumes and validates the golden policy. Tart
// guests keep their ECID across clones, so their encryption at rest is allowed.
func DiskPolicy(ctx context.Context, g Guest, allowEncryptionAtRest bool) (DiskEvidence, error) {
	out, err := g.Run(ctx, "/bin/bash -c "+Quote(diskPolicyProbe), "")
	if err != nil {
		return DiskEvidence{}, domain.Err("disk_policy_unknown", "Cannot read APFS encryption state; image will not be published")
	}
	evidence, err := ParseDiskEvidence(out)
	if err != nil {
		return evidence, err
	}
	return evidence, evidence.Validate(allowEncryptionAtRest)
}
```

Replace the body of `internal/images/disk_policy.go` (keep package, imports adjusted) with:

```go
// verifyDiskPolicy logs parsed evidence before enforcing the Lume policy.
func (e *Engine) verifyDiskPolicy(ctx context.Context, l domain.Lease, g *guest) (qualify.DiskEvidence, error) {
	evidence, err := qualify.DiskPolicy(ctx, g, l.LegacyUUID != "")
	var failure *domain.Error
	if errors.As(err, &failure) && failure.Code == "disk_policy_unknown" {
		return evidence, err
	}
	encoded, marshalErr := json.Marshal(evidence)
	if marshalErr != nil {
		return evidence, marshalErr
	}
	if logErr := e.provisionLog(l, "disk-policy", string(encoded)); logErr != nil {
		return evidence, logErr
	}
	return evidence, err
}
```

- [x] **Step 5: Create `internal/qualify/desktop.go`**

```go
package qualify

import (
	"context"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// DesktopProbe reports assistant, desktop or waiting.
const DesktopProbe = "if /usr/bin/pgrep -x 'Setup Assistant' >/dev/null; then echo assistant; elif test -f /var/db/.AppleSetupDone && /usr/bin/pgrep -x Finder >/dev/null; then echo desktop; else echo waiting; fi"
```

Then move `waitDesktop` from `internal/images/engine.go` verbatim into this file as `WaitDesktop`, and add:

```go
// Desktop requires a settled Finder desktop within two minutes. Setup
// Assistant is a failure here: adopted images never automate it.
func Desktop(ctx context.Context, g Guest) error {
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	state, err := WaitDesktop(wait, 2*time.Second, 5*time.Second, func(ctx context.Context) (string, error) {
		return g.Run(ctx, DesktopProbe, "")
	})
	if err != nil {
		return err
	}
	if state != "desktop" {
		return domain.Err("assistant_incomplete", "Guest shows Setup Assistant instead of a verified Finder desktop")
	}
	return nil
}
```

In `internal/images/engine.go`, delete `waitDesktop` and change `waitDesktopState` to:

```go
func (e *Engine) waitDesktopState(ctx context.Context, g *guest) (string, error) {
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return qualify.WaitDesktop(wait, 2*time.Second, 5*time.Second, func(ctx context.Context) (string, error) {
		return g.Run(ctx, qualify.DesktopProbe, "")
	})
}
```

Add `"github.com/mallexxx/virfield/internal/qualify"` to the imports of both changed `images` files; run `goimports`-style cleanup by hand (remove unused `strings` if the compiler reports it).

- [x] **Step 6: Run the tests**

Run: `go vet ./internal/qualify/ ./internal/images/ && go test ./internal/qualify/ ./internal/images/ 2>&1 | tail -3`
Expected: both `ok`.

- [x] **Step 7: Commit**

```bash
git add internal/qualify/ internal/images/
git commit -m "Extract disk policy and desktop checks into qualify"
```

---

### Task 4: `qualify` guest and Xcode checks

**Files:**
- Create: `internal/qualify/guest.go`, `internal/qualify/xcode.go`
- Test: `internal/qualify/guest_test.go`
- Modify: `internal/images/engine.go` (verify step), `internal/images/provision.go`, `internal/images/xcode.go`

- [x] **Step 1: Write the failing tests**

Create `internal/qualify/guest_test.go`:

```go
package qualify

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type fakeGuest struct {
	outputs  map[string]string
	commands []string
	inputs   []string
}

func (g *fakeGuest) Run(_ context.Context, command, input string) (string, error) {
	g.commands = append(g.commands, command)
	g.inputs = append(g.inputs, input)
	for prefix, out := range g.outputs {
		if strings.HasPrefix(command, prefix) {
			return out, nil
		}
	}
	return "", domain.Err("guest_command_failed", "unexpected command")
}

func (g *fakeGuest) RunReader(ctx context.Context, _ time.Duration, command string, input io.Reader) (string, error) {
	return g.Run(ctx, command, "")
}

func code(err error) string {
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

func TestOSRequiresExactBuildAndVersion(t *testing.T) {
	g := &fakeGuest{outputs: map[string]string{"/usr/bin/sw_vers -buildVersion": "22H420\n", "/usr/bin/sw_vers -productVersion": "13.7.4\n"}}
	if v, err := OS(context.Background(), g, "13.7.4", "22H420"); err != nil || v != "13.7.4" {
		t.Fatal(v, err)
	}
	if _, err := OS(context.Background(), g, "13.7.4", "22G120"); code(err) != "guest_build_mismatch" {
		t.Fatal(err)
	}
	if _, err := OS(context.Background(), g, "13.6", "22H420"); code(err) != "guest_version_mismatch" {
		t.Fatal(err)
	}
}

func TestSIPRequiresCanonicalState(t *testing.T) {
	g := &fakeGuest{outputs: map[string]string{"/usr/bin/csrutil status": "System Integrity Protection status: disabled.\n"}}
	if _, err := SIP(context.Background(), g, true); err != nil {
		t.Fatal(err)
	}
	if _, err := SIP(context.Background(), g, false); code(err) != "sip_verification_failed" {
		t.Fatal(err)
	}
}

func TestPasswordUsesConfiguredUserAndStdin(t *testing.T) {
	g := &fakeGuest{outputs: map[string]string{"/bin/bash -c": ""}}
	if err := Password(context.Background(), g, "admin", "vf-secret"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.commands[0], "-authonly admin ") || strings.Contains(g.commands[0], "vf-secret") || g.inputs[0] != "vf-secret\n" {
		t.Fatal(g.commands, g.inputs)
	}
	if err := Password(context.Background(), g, "admin'x", "p"); code(err) != "credential_verification_failed" {
		t.Fatal(err)
	}
}

func TestXcodeProbeUsesConfiguredUser(t *testing.T) {
	x := domain.XcodeRelease{Version: "15.2", Build: "15C500b"}
	g := &fakeGuest{outputs: map[string]string{
		"/usr/libexec/PlistBuddy":  "15.2\n15C500b\n",
		"/usr/bin/codesign":        "",
		"/usr/bin/xcodebuild -version": "Xcode 15.2\nBuild version 15C500b\n",
		"/bin/bash -c":             "ok",
	}}
	if _, err := Xcode(context.Background(), g, "admin", x); err != nil {
		t.Fatal(err)
	}
	probe := g.commands[len(g.commands)-1]
	if !strings.Contains(probe, "id -Gn admin") || strings.Contains(probe, "lume") {
		t.Fatal(probe)
	}
}
```

- [x] **Step 2: Run them to verify they fail**

Run: `go test ./internal/qualify/ 2>&1 | tail -3`
Expected: FAIL — `undefined: OS`, `SIP`, `Password`, `Xcode`.

- [x] **Step 3: Create `internal/qualify/guest.go`**

```go
package qualify

import (
	"context"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// OS verifies the exact build and, when given, the version; it returns the
// reported version for evidence and DeviceSupport naming.
func OS(ctx context.Context, g Guest, version, build string) (string, error) {
	actualBuild, err := g.Run(ctx, "/usr/bin/sw_vers -buildVersion", "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(actualBuild) != build {
		return "", domain.Err("guest_build_mismatch", "Guest macOS build differs from the image manifest")
	}
	out, err := g.Run(ctx, "/usr/bin/sw_vers -productVersion", "")
	if err != nil {
		return "", err
	}
	actual := strings.TrimSpace(out)
	if !domain.ValidVersion(actual) {
		return "", domain.Err("guest_version_mismatch", "Guest reported an invalid macOS version")
	}
	if version != "" && domain.CompareVersions(actual, version) != 0 {
		return "", domain.Err("guest_version_mismatch", "Guest macOS version differs from the selected catalog release")
	}
	return actual, nil
}

// SIP verifies the canonical normal-boot SIP status and returns it.
func SIP(ctx context.Context, g Guest, disabled bool) (string, error) {
	out, err := g.Run(ctx, "/usr/bin/csrutil status", "")
	if err != nil {
		return "", err
	}
	expected := "System Integrity Protection status: enabled."
	if disabled {
		expected = "System Integrity Protection status: disabled."
	}
	if strings.TrimSpace(out) != expected {
		return "", domain.Err("sip_verification_failed", "Guest SIP status does not match the requested canonical state")
	}
	return expected, nil
}

// Password proves the rotated account password persisted across a reboot.
// The password travels on stdin; the user is a validated name.
func Password(ctx context.Context, g Guest, user, password string) error {
	failure := domain.Err("credential_verification_failed", "Image password did not persist across reboot; image will not be published")
	if !domain.ValidName(user) {
		return failure
	}
	if _, err := g.Run(ctx, "/bin/bash -c 'IFS= read -r password; /usr/bin/dscl . -authonly "+user+" \"$password\"'", password+"\n"); err != nil {
		return failure
	}
	return nil
}
```

- [x] **Step 4: Create `internal/qualify/xcode.go`**

```go
package qualify

import (
	"context"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// AppleCodeRequirement accepts only Apple-signed code.
const AppleCodeRequirement = "=anchor apple"
```

Move `verifyGuestXcodeBundle` from `internal/images/provision.go` here verbatim as `XcodeBundle(ctx context.Context, g Guest, x domain.XcodeRelease) error`, replacing `shellQuote(appleCodeRequirement)` with `Quote(AppleCodeRequirement)`. Then add `Xcode`, built from the body of `Engine.verifyXcode` (`internal/images/provision.go:305-335`) with two changes — the user parameter and returning the probe output instead of logging it:

```go
// Xcode verifies the bundle, the toolchain and the debugging prerequisites.
// It returns the probe output (for the caller's private log) once the probe ran.
func Xcode(ctx context.Context, g Guest, user string, x domain.XcodeRelease) (string, error) {
	if !domain.ValidName(user) {
		return "", domain.Err("xcode_verification_failed", "Guest user name is invalid")
	}
	if err := XcodeBundle(ctx, g, x); err != nil {
		return "", err
	}
	out, err := g.Run(ctx, "/usr/bin/xcodebuild -version", "")
	if err != nil || strings.TrimSpace(out) != "Xcode "+x.Version+"\nBuild version "+x.Build {
		return "", domain.Err("xcode_version_mismatch", "Guest Xcode version/build does not match the manifest")
	}
	probe := `set -eu
 test "$(/usr/bin/xcode-select -p)" = /Applications/Xcode.app/Contents/Developer
 /usr/bin/xcodebuild -checkFirstLaunchStatus
 /usr/bin/xcrun --find swiftc
 sudo -n /usr/sbin/DevToolsSecurity -status 2>&1 | grep -q 'currently enabled'
 id -Gn ` + user + ` | tr ' ' '\n' | grep -qx _developer
 sudo -n /sbin/nfsd status
 grep -Eq '/(Cryptexes/OS/)?System/Library/dyld' /etc/exports
 probe_dir="$(/usr/bin/mktemp -d /tmp/virfield-swift.XXXXXX)"
 trap '/bin/rm -rf "$probe_dir"' EXIT
 printf '%s\n' 'import Foundation' 'print("virfield-swift-ok")' > "$probe_dir/probe.swift"
 /usr/bin/xcrun swiftc "$probe_dir/probe.swift" -o "$probe_dir/probe"
 test "$("$probe_dir/probe")" = virfield-swift-ok
 `
	out, err = g.RunReader(ctx, 5*time.Minute, "/bin/bash -c "+Quote(probe), nil)
	if err != nil {
		return out, domain.Err("xcode_verification_failed", "Guest Xcode signature, first launch or Swift compilation check failed")
	}
	return out, nil
}
```

- [x] **Step 5: Switch `internal/images` to the extracted checks**

- `internal/images/xcode.go:26`: `const appleCodeRequirement = qualify.AppleCodeRequirement` (add the import).
- `internal/images/provision.go`: delete `verifyGuestXcodeBundle`; replace its three call sites with `qualify.XcodeBundle(ctx, g, …)`; replace `Engine.verifyXcode` with:

```go
func (e *Engine) verifyXcode(ctx context.Context, l domain.Lease, g *guest, x domain.XcodeRelease) error {
	out, err := qualify.Xcode(ctx, g, "lume", x)
	if out != "" {
		if logErr := e.provisionLog(l, "xcode-verify", out); logErr != nil {
			return logErr
		}
	}
	return err
}
```

- `internal/images/engine.go` verify step: replace the build/version block (from `build, err := guest.Run(ctx, "/usr/bin/sw_vers -buildVersion", "")` through the version comparison) with

```go
		actualVersion, err := qualify.OS(ctx, guest, p.MacOS, p.Build)
		if err != nil {
			return err
		}
```

the `csrutil` block with

```go
		sip, err := qualify.SIP(ctx, guest, p.DisableSIP)
		if err != nil {
			return err
		}
```

and the `dscl -authonly lume` block with

```go
		if err := qualify.Password(ctx, secured, "lume", credentials.Password); err != nil {
			return err
		}
```

The evidence map keeps `"sip": strings.TrimSpace(sip)` working unchanged (the value is now already trimmed).

- [x] **Step 6: Run the tests**

Run: `go vet ./internal/qualify/ ./internal/images/ && go test ./internal/qualify/ ./internal/images/ 2>&1 | tail -3`
Expected: both `ok`. `TestMontereyXIPTransferChecksDigestAndSignatureBeforeReplacingApp` still passes (it inspects the script strings that use `appleCodeRequirement`).

- [x] **Step 7: Commit**

```bash
git add internal/qualify/ internal/images/
git commit -m "Extract guest and Xcode checks into qualify"
```

---

### Task 5: Move DeviceSupport into `qualify`

**Files:**
- Move: `internal/images/devicesupport.go` → `internal/qualify/devicesupport.go`, `internal/images/devicesupport_test.go` → `internal/qualify/devicesupport_test.go`
- Modify: `internal/images/engine.go:284`

- [x] **Step 1: Move code and tests**

```bash
git mv internal/images/devicesupport.go internal/qualify/devicesupport.go
git mv internal/images/devicesupport_test.go internal/qualify/devicesupport_test.go
sed -i '' 's/^package images$/package qualify/' internal/qualify/devicesupport.go internal/qualify/devicesupport_test.go
sed -i '' 's/^func (e \*Engine) prepareDeviceSupport(ctx context.Context, guestIP/func DeviceSupport(ctx context.Context, dir, guestIP/; s/e\.Dir/dir/g' internal/qualify/devicesupport.go
```

Add a doc comment above `DeviceSupport`:

```go
// DeviceSupport extracts the golden's dyld shared cache over its read-only NFS
// export into the host's Xcode DeviceSupport, so host LLDB can symbolicate.
// dir is the daemon state directory used for the extractor cache and log.
```

- [x] **Step 2: Update the Lume call site**

`internal/images/engine.go:284`:

```go
			deviceSupport, err = qualify.DeviceSupport(ctx, e.Dir, rebooted.IP, actualVersion, p.Build, progress)
```

- [x] **Step 3: Run the tests**

Run: `go vet ./internal/... && go test ./internal/qualify/ ./internal/images/ 2>&1 | tail -3`
Expected: both `ok` (`TestDeviceSupportPath*` now run in `qualify`).

- [x] **Step 4: Commit**

```bash
git add internal/qualify/ internal/images/
git commit -m "Move DeviceSupport preparation into qualify"
```

---

### Task 6: Embedded Packer recipe

**Files:**
- Create: `internal/tartimage/recipe/developer.pkr.hcl`, `internal/tartimage/recipe/scripts/check-guest.sh`, `internal/tartimage/recipe/scripts/install-xcode.sh`, `internal/tartimage/recipe/scripts/xcode-post.sh`, `internal/tartimage/recipe.go`
- Test: `internal/tartimage/recipe_test.go`

- [x] **Step 1: Write the failing test**

```go
package tartimage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeDigestIsStableAndPinned(t *testing.T) {
	if RecipeDigest() != RecipeDigest() || len(RecipeDigest()) != 64 {
		t.Fatal("unstable recipe digest")
	}
	template, err := recipe.ReadFile("recipe/developer.pkr.hcl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`version = "= 1.21.0"`, `recovery_partition = "relocate"`, "disk_size_gb = 120"} {
		if !strings.Contains(string(template), want) {
			t.Fatalf("recipe lacks %s", want)
		}
	}
}

func TestRecipeScriptsParseAndMaterializePrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recipe")
	if err := writeRecipe(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check-guest.sh", "install-xcode.sh", "xcode-post.sh"} {
		path := filepath.Join(dir, "scripts", name)
		if out, err := exec.Command("/bin/bash", "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("%s: %s %v", name, out, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("%s is not private: %v %v", name, info.Mode(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "developer.pkr.hcl")); err != nil {
		t.Fatal(err)
	}
}
```

- [x] **Step 2: Run it to verify it fails**

Run: `go test ./internal/tartimage/ 2>&1 | tail -3`
Expected: FAIL — `no non-test Go files` / `undefined: RecipeDigest`.

- [x] **Step 3: Create the recipe files**

`internal/tartimage/recipe/developer.pkr.hcl`:

```hcl
# Virfield developer-v1 recipe for Cirrus Tart images. Derived from
# cirruslabs/macos-image-templates (MIT). Inputs are fixed by the image job.
packer {
  required_plugins {
    tart = {
      version = "= 1.21.0"
      source  = "github.com/cirruslabs/tart"
    }
  }
}

variable "base" { type = string }
variable "vm_name" { type = string }
variable "macos_version" { type = string }
variable "macos_build" { type = string }
variable "sip" { type = string }
variable "xip" { type = string }
variable "xip_sha1" { type = string }
variable "xcode_version" { type = string }
variable "xcode_build" { type = string }

source "tart-cli" "developer" {
  vm_base_name       = var.base
  vm_name            = var.vm_name
  cpu_count          = 4
  memory_gb          = 8
  disk_size_gb = 120
  recovery_partition = "relocate"
  headless           = true
  ssh_username       = "admin"
  ssh_password       = "admin"
  ssh_timeout        = "180s"
}

build {
  sources = ["source.tart-cli.developer"]

  provisioner "shell" {
    script           = "scripts/check-guest.sh"
    environment_vars = ["VF_MACOS=${var.macos_version}", "VF_BUILD=${var.macos_build}", "VF_SIP=${var.sip}"]
  }
  provisioner "shell" {
    inline = ["umask 077", "mkdir -p \"$HOME/.virfield-xcode\""]
  }
  provisioner "file" {
    source      = var.xip
    destination = "/Users/admin/.virfield-xcode/archive.xip"
  }
  provisioner "shell" {
    script           = "scripts/install-xcode.sh"
    timeout          = "2h"
    environment_vars = ["VF_XIP_SHA1=${var.xip_sha1}", "VF_XCODE_VERSION=${var.xcode_version}", "VF_XCODE_BUILD=${var.xcode_build}"]
  }
  provisioner "shell" {
    script  = "scripts/xcode-post.sh"
    timeout = "1h"
  }
}
```

`internal/tartimage/recipe/scripts/check-guest.sh`:

```bash
#!/bin/bash
# Fail fast before any mutation if the pinned upstream is not what the profile says.
set -euo pipefail
case "$(sysctl -n hw.model)" in VirtualMac*) ;; *) echo 'not a virtual Mac'; exit 64 ;; esac
version="$(sw_vers -productVersion)"
build="$(sw_vers -buildVersion)"
[[ "$version" == "$VF_MACOS" ]] || { echo "macOS $version, expected $VF_MACOS"; exit 65; }
[[ "$build" == "$VF_BUILD" ]] || { echo "build $build, expected $VF_BUILD"; exit 65; }
[[ "$(csrutil status)" == "System Integrity Protection status: $VF_SIP." ]] || { csrutil status; exit 65; }
sudo -n true
available_kib="$(df -k / | awk 'NR==2 {print $4}')"
(( available_kib >= 40 * 1024 * 1024 )) || { echo "only ${available_kib} KiB free"; exit 66; }
echo "guest matches profile: $version ($build), SIP $VF_SIP"
```

`internal/tartimage/recipe/scripts/install-xcode.sh`:

```bash
#!/bin/bash
# Verify the uploaded archive, then Apple's signature and the exact release
# before replacing /Applications/Xcode.app.
set -euo pipefail
archive="$HOME/.virfield-xcode/archive.xip"
work="$HOME/.virfield-xcode/expanded"
trap 'rm -rf "$work" "$archive"' EXIT
actual="$(shasum -a 1 "$archive")"
[[ "${actual%% *}" == "$VF_XIP_SHA1" ]] || { echo 'XIP digest mismatch'; exit 65; }
rm -rf "$work"
mkdir -p "$work"
cd "$work"
xip --expand "$archive"
app="$work/Xcode.app"
test -x "$app/Contents/Developer/usr/bin/xcodebuild"
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/version.plist")" == "$VF_XCODE_VERSION" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :ProductBuildVersion' "$app/Contents/version.plist")" == "$VF_XCODE_BUILD" ]]
codesign --verify --deep --strict -R '=anchor apple' "$app"
sudo -n rm -rf /Applications/Xcode.app
sudo -n mv "$app" /Applications/Xcode.app
test -x /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild
```

`internal/tartimage/recipe/scripts/xcode-post.sh` (port of the Lume `developer-v1` post-install, user from the session):

```bash
#!/bin/bash
set -euo pipefail
user="$(id -un)"
sudo -n /usr/bin/xcode-select -s /Applications/Xcode.app
sudo -n /usr/bin/xcodebuild -license accept
sudo -n /usr/bin/xcodebuild -runFirstLaunch
sudo -n /usr/sbin/DevToolsSecurity -enable
sudo -n /usr/sbin/dseditgroup -o edit -a "$user" -t user _developer
# Host LLDB symbolication reads the guest dyld cache over a read-only export.
dyld_cache=
for candidate in /System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld /System/Library/dyld; do
  if test -d "$candidate"; then
    dyld_cache="$candidate"
    break
  fi
done
test -n "$dyld_cache"
dyld_export="\"$dyld_cache\" -ro -mapall=$(id -u):$(id -g) -network 192.168.64.0 -mask 255.255.255.0"
sudo -n touch /etc/exports
sudo -n grep -Fqx "$dyld_export" /etc/exports || printf '%s\n' "$dyld_export" | sudo -n tee -a /etc/exports >/dev/null
sudo -n /sbin/nfsd enable
sudo -n /sbin/nfsd restart
/usr/bin/xcodebuild -version
```

- [x] **Step 4: Create `internal/tartimage/recipe.go`**

```go
// Package tartimage builds and qualifies Tart goldens from pinned Cirrus
// images with Packer and Virfield's guest qualification.
package tartimage

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed recipe
var recipe embed.FS

// RecipeDigest identifies the exact embedded recipe for image evidence.
func RecipeDigest() string {
	h := sha256.New()
	_ = fs.WalkDir(recipe, "recipe", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := recipe.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(b))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// writeRecipe materializes the recipe into a fresh private directory.
func writeRecipe(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return fs.WalkDir(recipe, "recipe", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, strings.TrimPrefix(path, "recipe"))
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		b, err := recipe.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0600)
	})
}
```

- [x] **Step 5: Run the tests**

Run: `gofmt -l internal/tartimage; go test ./internal/tartimage/ -v 2>&1 | tail -6`
Expected: `PASS` for both tests. (Packer runs `bash` on the uploaded copy, so mode 0600 scripts are fine.)

- [x] **Step 6: Validate the template with Packer**

Run: `cd "$(mktemp -d)" && cp -R /Users/admin/Developer/virfield-v3/internal/tartimage/recipe/. . && packer init developer.pkr.hcl && packer validate -syntax-only developer.pkr.hcl; echo exit=$?`
Expected: `The configuration is valid.` and `exit=0`. If `packer init` reports that version `1.21.0` does not exist, run `packer plugins installed` and pin the newest listed release instead, in both the template and the test.

- [x] **Step 7: Commit**

```bash
git add internal/tartimage/
git commit -m "Add embedded Packer recipe for Tart developer images"
```

---

### Task 7: Builder resolve, build and stop steps

**Files:**
- Create: `internal/tartimage/builder.go`
- Test: `internal/tartimage/builder_test.go`

- [x] **Step 1: Write the failing tests**

```go
package tartimage

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/tart"
)

type call struct {
	dir  string
	env  []string
	args []string
}

type fakeRunner struct {
	calls []call
	err   error
}

func (r *fakeRunner) Run(_ context.Context, dir string, env []string, _ string, _ string, args ...string) error {
	r.calls = append(r.calls, call{dir, env, args})
	return r.err
}

type fakeRuntime struct {
	mu      sync.Mutex
	state   string
	ip      string
	starts  int
	stops   int
}

func (f *fakeRuntime) Observe(context.Context) (domain.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == "" {
		return domain.Observation{}, nil
	}
	return domain.Observation{VMs: []domain.VM{{Name: "ventura-golden", Location: "samsung", State: f.state, IP: f.ip}}}, nil
}
func (f *fakeRuntime) Start(context.Context, domain.Lease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.state, f.ip = "running", "192.168.64.20"
	return nil
}
func (f *fakeRuntime) Stop(context.Context, domain.Lease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.state, f.ip = "stopped", ""
	return nil
}

func testBuilder(t *testing.T) (*Builder, *fakeRunner, *fakeRuntime, domain.Lease, domain.ImageProfile) {
	t.Helper()
	archives := t.TempDir()
	content := []byte("not really an xip")
	sum := sha1.Sum(content)
	p := domain.ImageProfile{
		Tart:       &domain.TartSource{Upstream: "ghcr.io/cirruslabs/macos-ventura-base@sha256:" + strings.Repeat("a", 64)},
		MacOS:      "13.7.4",
		Build:      "22H420",
		Security:   "sip-disabled",
		DisableSIP: true,
		Provision:  "developer-v1",
		Xcode:      &domain.XcodeRelease{Version: "15.2", Build: "15C500b", Requires: "13.5", SHA1: hex.EncodeToString(sum[:])},
	}
	if err := os.WriteFile(filepath.Join(archives, p.Xcode.SHA1+".xip"), content, 0600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	runtime := &fakeRuntime{}
	b := &Builder{
		config: Config{
			StateDir:      t.TempDir(),
			Packer:        "/opt/homebrew/bin/packer",
			XcodeArchives: archives,
			Homes:         map[string]string{"samsung": "/Volumes/S/tart"},
			Credentials:   []tart.RegistryCredential{{Host: "ghcr.io", Username: "operator", Token: "secret"}},
			Runtime:       runtime,
		},
		run: runner,
	}
	l := domain.Lease{ID: "image-test", Purpose: "image", VMName: "ventura-golden", Location: "samsung", GuestUser: "admin"}
	return b, runner, runtime, l, p
}

func noProgress(string) error { return nil }

func TestResolveVerifiesArchiveAndInitializesPinnedPlugin(t *testing.T) {
	b, runner, _, l, p := testBuilder(t)
	if err := b.Step(context.Background(), l, p, "resolve", noProgress); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || strings.Join(runner.calls[0].args, " ") != "init developer.pkr.hcl" {
		t.Fatal(runner.calls)
	}
	if digest, err := os.ReadFile(filepath.Join(b.config.StateDir, "images", l.ID, "recipe.sha256")); err != nil || string(digest) != RecipeDigest() {
		t.Fatal("recipe digest not recorded", err)
	}
	p.Xcode.SHA1 = strings.Repeat("0", 40)
	if err := b.Step(context.Background(), l, p, "resolve", noProgress); !hasCode(err, "unsafe_cache") {
		t.Fatal(err)
	}
}

func TestBuildPassesPinnedInputsAndScopedCredentials(t *testing.T) {
	b, runner, _, l, p := testBuilder(t)
	if err := b.Step(context.Background(), l, p, "resolve", noProgress); err != nil {
		t.Fatal(err)
	}
	if err := b.Step(context.Background(), l, p, "build", noProgress); err != nil {
		t.Fatal(err)
	}
	build := runner.calls[1]
	args := strings.Join(build.args, " ")
	for _, want := range []string{"build", "-var base=" + p.Tart.Upstream, "-var vm_name=ventura-golden", "-var macos_build=22H420", "-var sip=disabled", "-var xcode_build=15C500b", "developer.pkr.hcl"} {
		if !strings.Contains(args, want) {
			t.Fatalf("build args lack %q: %s", want, args)
		}
	}
	env := strings.Join(build.env, "\n")
	for _, want := range []string{"TART_HOME=/Volumes/S/tart", "TART_REGISTRY_HOSTNAME=ghcr.io", "TART_REGISTRY_PASSWORD=secret", "CHECKPOINT_DISABLE=1", "PACKER_PLUGIN_PATH=" + filepath.Join(b.config.StateDir, "packer-plugins")} {
		if !strings.Contains(env, want) {
			t.Fatalf("build env lacks %q", want)
		}
	}
	runner.err = errors.New("exit status 1")
	if err := b.Step(context.Background(), l, p, "build", noProgress); !hasCode(err, "image_command_failed") {
		t.Fatal(err)
	}
}

func TestStopConfirmsStoppedVM(t *testing.T) {
	b, _, runtime, l, p := testBuilder(t)
	runtime.state = "running"
	if err := b.Step(context.Background(), l, p, "stop", noProgress); err != nil || runtime.stops != 1 {
		t.Fatal(runtime.stops, err)
	}
	if err := b.Step(context.Background(), l, p, "unknown", noProgress); !hasCode(err, "invalid_profile") {
		t.Fatal(err)
	}
}

func hasCode(err error, want string) bool {
	var failure *domain.Error
	return errors.As(err, &failure) && failure.Code == want
}
```

- [x] **Step 2: Run them to verify they fail**

Run: `go test ./internal/tartimage/ 2>&1 | tail -3`
Expected: FAIL — `undefined: Builder`, `Config`.

- [x] **Step 3: Implement `internal/tartimage/builder.go`**

```go
package tartimage

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/tart"
)

// Runtime is the subset of the Tart runtime client the builder drives.
type Runtime interface {
	Observe(context.Context) (domain.Observation, error)
	Start(context.Context, domain.Lease) error
	Stop(context.Context, domain.Lease) error
}

type runner interface {
	Run(ctx context.Context, dir string, env []string, log, name string, args ...string) error
}

type Config struct {
	StateDir      string
	Packer        string
	XcodeArchives string
	// Homes maps a Tart container ID to its TART_HOME.
	Homes       map[string]string
	Credentials []tart.RegistryCredential
	Runtime     Runtime
}

type Builder struct {
	config        Config
	run           runner
	guests        guests
	deviceSupport func(ctx context.Context, dir, ip, version, build string, progress func(string) error) (string, error)
}

func New(config Config) *Builder {
	manager := &guestssh.Manager{Dir: config.StateDir}
	return &Builder{config: config, run: execRunner{}, guests: sshGuests{manager}, deviceSupport: qualifyDeviceSupport}
}

func (b *Builder) Step(ctx context.Context, l domain.Lease, p domain.ImageProfile, step string, progress func(string) error) error {
	if p.Tart == nil || p.Xcode == nil {
		return domain.Err("invalid_profile", "Tart builder requires a Tart source and a pinned Xcode release")
	}
	home, ok := b.config.Homes[l.Location]
	if !ok {
		return domain.Err("invalid_profile", "Image location is not a configured Tart container")
	}
	switch step {
	case "resolve":
		return b.resolve(ctx, l, p)
	case "build":
		return b.build(ctx, l, p, home)
	case "qualify":
		return b.qualify(ctx, l, p, progress)
	case "stop":
		return b.stop(ctx, l)
	}
	return domain.Err("invalid_profile", "unsupported Tart image stage")
}

func (b *Builder) folder(l domain.Lease) string { return filepath.Join(b.config.StateDir, "images", l.ID) }
func (b *Builder) archive(p domain.ImageProfile) string {
	return filepath.Join(b.config.XcodeArchives, p.Xcode.SHA1+".xip")
}

func (b *Builder) resolve(ctx context.Context, l domain.Lease, p domain.ImageProfile) error {
	if err := verifyArchive(b.archive(p), p.Xcode.SHA1); err != nil {
		return err
	}
	folder := b.folder(l)
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	if err := writeRecipe(filepath.Join(folder, "recipe")); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(folder, "recipe.sha256"), []byte(RecipeDigest()), 0600); err != nil {
		return err
	}
	if err := b.packer(ctx, l, nil, "packer-init.log", "init", "developer.pkr.hcl"); err != nil {
		return domain.Err("image_command_failed", "Packer plugin initialization failed; inspect the private packer-init log")
	}
	return nil
}

func (b *Builder) build(ctx context.Context, l domain.Lease, p domain.ImageProfile, home string) error {
	sip := "enabled"
	if p.DisableSIP {
		sip = "disabled"
	}
	vars := []string{
		"base=" + p.Tart.Upstream,
		"vm_name=" + l.VMName,
		"macos_version=" + p.MacOS,
		"macos_build=" + p.Build,
		"sip=" + sip,
		"xip=" + b.archive(p),
		"xip_sha1=" + p.Xcode.SHA1,
		"xcode_version=" + p.Xcode.Version,
		"xcode_build=" + p.Xcode.Build,
	}
	args := []string{"build"}
	for _, v := range vars {
		args = append(args, "-var", v)
	}
	args = append(args, "developer.pkr.hcl")
	env := append([]string{"TART_HOME=" + home}, b.registryEnv(p.Tart.Upstream)...)
	if err := b.packer(ctx, l, env, "packer-build.log", args...); err != nil {
		return domain.Err("image_command_failed", "Packer build failed; inspect the private packer-build log")
	}
	return nil
}

// registryEnv scopes a configured credential to its registry, as the Tart
// runtime does; the host Docker credential is never relied on.
func (b *Builder) registryEnv(ref string) []string {
	for _, c := range b.config.Credentials {
		if strings.HasPrefix(ref, c.Host+"/") {
			return []string{"TART_REGISTRY_HOSTNAME=" + c.Host, "TART_REGISTRY_USERNAME=" + c.Username, "TART_REGISTRY_PASSWORD=" + c.Token}
		}
	}
	return nil
}

func (b *Builder) packer(ctx context.Context, l domain.Lease, env []string, log string, args ...string) error {
	base := []string{"CHECKPOINT_DISABLE=1", "PACKER_PLUGIN_PATH=" + filepath.Join(b.config.StateDir, "packer-plugins")}
	return b.run.Run(ctx, filepath.Join(b.folder(l), "recipe"), append(base, env...), filepath.Join(b.folder(l), log), b.config.Packer, args...)
}

func (b *Builder) stop(ctx context.Context, l domain.Lease) error {
	vm, ok, err := b.vm(ctx, l)
	if err != nil {
		return err
	}
	if ok && vm.State != "stopped" {
		if err := b.config.Runtime.Stop(ctx, l); err != nil {
			return err
		}
	}
	return b.waitState(ctx, l, "stopped")
}

func (b *Builder) vm(ctx context.Context, l domain.Lease) (domain.VM, bool, error) {
	o, err := b.config.Runtime.Observe(ctx)
	if err != nil {
		return domain.VM{}, false, err
	}
	for _, vm := range o.VMs {
		if vm.Name == l.VMName && vm.Location == l.Location {
			return vm, true, nil
		}
	}
	return domain.VM{}, false, nil
}

// waitState polls the runtime; "running" additionally requires an IP.
func (b *Builder) waitState(ctx context.Context, l domain.Lease, state string) error {
	wait, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for {
		vm, ok, err := b.vm(wait, l)
		if err != nil {
			return err
		}
		if ok && vm.State == state && (state != "running" || vm.IP != "") {
			return nil
		}
		select {
		case <-wait.Done():
			return domain.Err("stop_timeout", "Image VM did not reach state "+state)
		case <-time.After(2 * time.Second):
		}
	}
}

func verifyArchive(path, digest string) error {
	f, err := os.Open(path)
	if err != nil {
		return domain.Err("unsafe_cache", "Pinned Xcode archive is not in the configured cache")
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return domain.Err("unsafe_cache", "Xcode archive is not a regular file")
	}
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return domain.Err("unsafe_cache", "Xcode archive digest differs from the pinned release")
	}
	return nil
}

type execRunner struct{}

// Run interrupts Packer on cancellation so it can stop and clean up its VM.
func (execRunner) Run(ctx context.Context, dir string, env []string, log, name string, args ...string) error {
	out, err := os.OpenFile(log, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 2 * time.Minute
	return cmd.Run()
}
```

The `guests`, `sshGuests`, `qualifyDeviceSupport` and `Builder.qualify` symbols are created in Task 8. To compile this task on its own, add a temporary stub at the bottom of `builder.go`:

```go
type guests interface{}
type sshGuests struct{ m *guestssh.Manager }

var qualifyDeviceSupport func(ctx context.Context, dir, ip, version, build string, progress func(string) error) (string, error)

func (b *Builder) qualify(context.Context, domain.Lease, domain.ImageProfile, func(string) error) error {
	return domain.Err("invalid_profile", "qualification is not implemented yet")
}
```

- [x] **Step 4: Run the tests**

Run: `go vet ./internal/tartimage/ && go test ./internal/tartimage/ -v 2>&1 | grep -E '^(---|ok|FAIL)'`
Expected: all `--- PASS`, then `ok`.

- [x] **Step 5: Commit**

```bash
git add internal/tartimage/
git commit -m "Add Tart image builder resolve, build and stop steps"
```

---

### Task 8: Builder qualify step

**Files:**
- Create: `internal/tartimage/qualify.go`
- Modify: `internal/tartimage/builder.go` (remove the Task 7 stub)
- Test: `internal/tartimage/qualify_test.go`

- [x] **Step 1: Write the failing test**

```go
package tartimage

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type fakeSession struct {
	runtime  *fakeRuntime
	commands *[]string
}

func (s fakeSession) Run(_ context.Context, command, _ string) (string, error) {
	*s.commands = append(*s.commands, command)
	switch {
	case strings.HasPrefix(command, "/usr/bin/sw_vers -buildVersion"):
		return "22H420\n", nil
	case strings.HasPrefix(command, "/usr/bin/sw_vers -productVersion"):
		return "13.7.4\n", nil
	case strings.HasPrefix(command, "/usr/bin/csrutil status"):
		return "System Integrity Protection status: disabled.\n", nil
	case strings.HasPrefix(command, "if /usr/bin/pgrep"):
		return "desktop\n", nil
	case strings.Contains(command, "diskutil info"):
		return "false\ntrue\nfalse\nfalse\ntrue\nfalse\n", nil
	case strings.HasPrefix(command, "/usr/libexec/PlistBuddy"):
		return "15.2\n15C500b\n", nil
	case strings.HasPrefix(command, "/usr/bin/xcodebuild -version"):
		return "Xcode 15.2\nBuild version 15C500b\n", nil
	case strings.HasPrefix(command, "sudo -n /sbin/shutdown"):
		s.runtime.Stop(context.Background(), domain.Lease{})
		return "", nil
	}
	return "", nil
}
func (s fakeSession) RunReader(ctx context.Context, _ time.Duration, command string, _ io.Reader) (string, error) {
	return s.Run(ctx, command, "")
}
func (fakeSession) Close() {}

type fakeGuests struct {
	runtime  *fakeRuntime
	commands []string
	connects []bool
	secured  int
}

func (g *fakeGuests) Connect(_ context.Context, _ domain.Lease, _ string, bootstrap bool) (session, error) {
	g.connects = append(g.connects, bootstrap)
	return fakeSession{g.runtime, &g.commands}, nil
}
func (g *fakeGuests) Secure(context.Context, domain.Lease, string, session) error { g.secured++; return nil }
func (g *fakeGuests) Password(domain.Lease) (string, error)                     { return "vf-rotated", nil }

func TestQualifySecuresRebootsVerifiesAndRecordsEvidence(t *testing.T) {
	b, _, runtime, l, p := testBuilder(t)
	runtime.state = "stopped"
	g := &fakeGuests{runtime: runtime}
	b.guests = g
	b.deviceSupport = func(context.Context, string, string, string, string, func(string) error) (string, error) {
		return "/Users/x/Library/Developer/Xcode/macOS DeviceSupport/13.7.4 (22H420)/Symbols", nil
	}
	if err := os.MkdirAll(b.folder(l), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.folder(l), "recipe.sha256"), []byte(RecipeDigest()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.Step(context.Background(), l, p, "qualify", noProgress); err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 2 || runtime.state != "stopped" || g.secured != 1 || len(g.connects) != 2 || !g.connects[0] || g.connects[1] {
		t.Fatalf("starts=%d state=%s secured=%d connects=%v", runtime.starts, runtime.state, g.secured, g.connects)
	}
	joined := strings.Join(g.commands, "\n")
	if !strings.Contains(joined, "-authonly admin ") || !strings.Contains(joined, "id -Gn admin") {
		t.Fatal("qualification did not use the template guest user")
	}
	var evidence map[string]any
	b2, err := os.ReadFile(filepath.Join(b.folder(l), "verification.json"))
	if err != nil || json.Unmarshal(b2, &evidence) != nil {
		t.Fatal(err)
	}
	if evidence["upstream"] != p.Tart.Upstream || evidence["recipe_sha256"] != RecipeDigest() || evidence["build"] != "22H420" {
		t.Fatal(evidence)
	}
}
```

- [x] **Step 2: Run it to verify it fails**

Run: `go test ./internal/tartimage/ -run TestQualify 2>&1 | tail -3`
Expected: FAIL to compile — `undefined: session`; the stub `guests` has no methods.

- [x] **Step 3: Implement `internal/tartimage/qualify.go`**

Delete the Task 7 stub block from `builder.go`, then create:

```go
package tartimage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/qualify"
)

type session interface {
	qualify.Guest
	Close()
}

// guests is the image credential lifecycle; sshGuests is the real one.
type guests interface {
	Connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (session, error)
	Secure(ctx context.Context, l domain.Lease, ip string, g session) error
	Password(l domain.Lease) (string, error)
}

type sshGuests struct{ m *guestssh.Manager }

func (s sshGuests) Connect(ctx context.Context, l domain.Lease, ip string, bootstrap bool) (session, error) {
	return s.m.ConnectReady(ctx, l, ip, bootstrap)
}
func (s sshGuests) Secure(ctx context.Context, l domain.Lease, ip string, g session) error {
	client, ok := g.(*guestssh.Client)
	if !ok {
		return domain.Err("guest_identity_failed", "Unexpected guest session")
	}
	return s.m.SecureImage(ctx, l, ip, client)
}
func (s sshGuests) Password(l domain.Lease) (string, error) {
	c, err := guestssh.LoadCredentials(s.m.CredentialPath(l))
	return c.Password, err
}

var qualifyDeviceSupport = qualify.DeviceSupport

func guestUser(l domain.Lease) string {
	if l.GuestUser == "" {
		return "admin"
	}
	return l.GuestUser
}

// qualify secures the image credential, reboots, and verifies the golden
// with the pinned host key before writing evidence and shutting down cleanly.
func (b *Builder) qualify(ctx context.Context, l domain.Lease, p domain.ImageProfile, progress func(string) error) error {
	recipeDigest, err := os.ReadFile(filepath.Join(b.folder(l), "recipe.sha256"))
	if err != nil {
		return domain.Err("invalid_profile", "Image has no recorded build recipe")
	}
	ip, err := b.boot(ctx, l)
	if err != nil {
		return err
	}
	bootstrap, err := b.guests.Connect(ctx, l, ip, true)
	if err != nil {
		return err
	}
	if err := qualify.Desktop(ctx, bootstrap); err != nil {
		bootstrap.Close()
		return err
	}
	if err := b.guests.Secure(ctx, l, ip, bootstrap); err != nil {
		bootstrap.Close()
		return err
	}
	if err := b.shutdown(ctx, l, bootstrap); err != nil {
		return err
	}
	if err := progress("Credentials rotated; verifying the image after reboot"); err != nil {
		return err
	}
	ip, err = b.boot(ctx, l)
	if err != nil {
		return err
	}
	g, err := b.guests.Connect(ctx, l, ip, false)
	if err != nil {
		return err
	}
	evidence, err := b.verify(ctx, l, p, g, ip, progress)
	if err != nil {
		g.Close()
		return err
	}
	evidence["upstream"] = p.Tart.Upstream
	evidence["recipe_sha256"] = string(recipeDigest)
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	if err := os.WriteFile(filepath.Join(b.folder(l), "verification.json"), encoded, 0600); err != nil {
		g.Close()
		return err
	}
	return b.shutdown(ctx, l, g)
}

func (b *Builder) verify(ctx context.Context, l domain.Lease, p domain.ImageProfile, g session, ip string, progress func(string) error) (map[string]any, error) {
	user := guestUser(l)
	version, err := qualify.OS(ctx, g, p.MacOS, p.Build)
	if err != nil {
		return nil, err
	}
	sip, err := qualify.SIP(ctx, g, p.DisableSIP)
	if err != nil {
		return nil, err
	}
	if err := qualify.Desktop(ctx, g); err != nil {
		return nil, err
	}
	disk, err := qualify.DiskPolicy(ctx, g, true)
	if err != nil {
		return nil, err
	}
	password, err := b.guests.Password(l)
	if err != nil {
		return nil, err
	}
	if err := qualify.Password(ctx, g, user, password); err != nil {
		return nil, err
	}
	out, err := qualify.Xcode(ctx, g, user, *p.Xcode)
	if out != "" {
		_ = os.WriteFile(filepath.Join(b.folder(l), "xcode-verify.log"), []byte(out), 0600)
	}
	if err != nil {
		return nil, err
	}
	support, err := b.deviceSupport(ctx, b.config.StateDir, ip, version, p.Build, progress)
	if err != nil {
		return nil, err
	}
	return map[string]any{"macos": version, "build": p.Build, "xcode": p.Xcode, "sip": sip, "desktop": true, "disk": disk, "scoped_image_ssh": true, "guest_user": user, "device_support": support, "provision": p.Provision, "security": p.Security, "verified_at": time.Now().UTC()}, nil
}

func (b *Builder) boot(ctx context.Context, l domain.Lease) (string, error) {
	if err := b.config.Runtime.Start(ctx, l); err != nil {
		return "", err
	}
	if err := b.waitState(ctx, l, "running"); err != nil {
		return "", err
	}
	vm, _, err := b.vm(ctx, l)
	return vm.IP, err
}

// shutdown asks the guest for a clean halt; the session drops as it stops.
func (b *Builder) shutdown(ctx context.Context, l domain.Lease, g session) error {
	_, _ = g.Run(ctx, "sudo -n /sbin/shutdown -h now", "")
	g.Close()
	return b.waitState(ctx, l, "stopped")
}
```

- [x] **Step 4: Run the tests**

Run: `go vet ./internal/tartimage/ && go test ./internal/tartimage/ -v 2>&1 | grep -E '^(---|ok|FAIL)'`
Expected: all `--- PASS`, `ok`.

- [x] **Step 5: Commit**

```bash
git add internal/tartimage/
git commit -m "Qualify Tart images after credential rotation and reboot"
```

---

### Task 9: Controller Tart phases

**Files:**
- Modify: `internal/control/image_build.go`
- Test: `internal/control/image_build_test.go`

- [x] **Step 1: Write the failing tests**

In `fakeImageBuilder.Step`, make the fake VM appear on Tart builds too — change `if step == "create" {` to `if step == "create" || step == "build" {`. Then append:

```go
func tartImageController(t *testing.T) (*Controller, *fakeImageBuilder) {
	c, b := imageController(t)
	p := domain.ImageProfile{Tart: &domain.TartSource{Upstream: "ghcr.io/cirruslabs/macos-ventura-base@sha256:" + strings.Repeat("a", 64)}, MacOS: "13.7.4", Build: "22H420", Security: "sip-disabled", DisableSIP: true, Provision: "developer-v1", Xcode: &domain.XcodeRelease{Version: "15.2", Build: "15C500b", Requires: "13.5", SHA1: strings.Repeat("b", 40)}}
	c.templates["test"] = domain.Template{ID: "test", Name: "golden", Location: "home", GuestUser: "admin", GuestHome: "/Users/admin", Image: &p}
	return c, b
}

func TestTartImageRunsTartPhasesAndPromotes(t *testing.T) {
	c, b := tartImageController(t)
	op, err := c.BuildImage(context.Background(), "test", "tart-image-build")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		tick(t, c)
	}
	if strings.Join(b.steps, ",") != "resolve,build,qualify,stop" {
		t.Fatal(b.steps)
	}
	l, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || l.State != "image_ready" || l.ImageManifest != imageFingerprint(c.templates["test"].Image) {
		t.Fatal(l.State, err)
	}
}

func TestTartQualifyFailureAllowsOnlyQualifyRetry(t *testing.T) {
	c, b := tartImageController(t)
	b.fail = "qualify"
	op, err := c.BuildImage(context.Background(), "test", "tart-image-fail")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		tick(t, c)
	}
	j, err := c.Job(context.Background(), op.Job.ID)
	if err != nil || j.State != "needs_attention" || j.Phase != "qualify_dispatched" {
		t.Fatal(j, err)
	}
	b.fail = ""
	tick(t, c)
	if _, err := c.RecoverImage(context.Background(), op.Lease.ID, "tart-retry-qualify", "golden", "retry", true); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		tick(t, c)
	}
	l, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || l.State != "image_ready" {
		t.Fatal(l.State, err)
	}
}
```

- [x] **Step 2: Run them to verify they fail**

Run: `go test ./internal/control/ -run TestTart -v 2>&1 | grep -E '^(---|ok|FAIL)|steps'`
Expected: FAIL — the steps start with `download` because the Lume phase map is used.

- [x] **Step 3: Implement**

In `internal/control/image_build.go`, below `imageNext` add:

```go
// tartImageNext adopts a pinned Tart image: Packer builds, Virfield qualifies.
var tartImageNext = map[string]string{"queued": "resolve", "resolve_done": "build", "build_done": "qualify", "qualify_done": "stop"}
```

In `advanceImageBuild`, replace

```go
	step, ok := imageNext[j.Phase]
	if !ok {
		return nil
	}
	if j.Phase == "sip_done" && j.Image.Provision != "" {
		step = "provision"
	}
	if step == "create" {
```

with

```go
	next := imageNext
	if j.Image.Tart != nil {
		next = tartImageNext
	}
	step, ok := next[j.Phase]
	if !ok {
		return nil
	}
	if j.Image.Tart == nil && j.Phase == "sip_done" && j.Image.Provision != "" {
		step = "provision"
	}
	if step == "create" || step == "build" {
```

Add three entries to the progress map literal (same line, after `"stop": …`):

```go
"resolve": "Verifying the pinned Tart source, Xcode archive and build recipe", "build": "Building the image with Packer from the pinned Tart source", "qualify": "Securing credentials and verifying build, SIP, desktop, disk policy and Xcode after reboot"
```

In `RecoverImage`, extend the `previous` map with `"resolve_dispatched": "queued", "qualify_dispatched": "build_done"`.

- [x] **Step 4: Run the control tests**

Run: `go test ./internal/control/ 2>&1 | tail -3`
Expected: `ok`.

- [x] **Step 5: Commit**

```bash
git add internal/control/
git commit -m "Run Tart image builds through Tart phases"
```

---

### Task 10: Wiring and operator documentation

**Files:**
- Modify: `cmd/virfieldd/main.go`, `docs/OPERATIONS.md`

- [x] **Step 1: Wire the builder**

In `cmd/virfieldd/main.go`, inside `case cfg.Runtime.Provider == "tart":`, keep a reference to the client and the credentials for later use. Change `backend, err = tart.New(…)` to:

```go
		tartClient, tartErr := tart.New(tart.Config{Binary: cfg.Runtime.Tart.Binary, HostID: cfg.Runtime.Tart.HostID, HostName: cfg.Runtime.Tart.HostName, MaxVMs: cfg.Runtime.Tart.MaxVMs, CPULimit: cfg.Runtime.Tart.CPULimit, MemoryReserveBytes: cfg.Runtime.Tart.MemoryReserveBytes, Containers: containers, RegistryCredentials: credentials})
		backend, err = tartClient, tartErr
		tartBackend, tartCredentials = tartClient, credentials
```

declaring `var tartBackend *tart.Client` and `var tartCredentials []tart.RegistryCredential` next to the existing `lumeBackend` declaration. Then, immediately after the existing `if cfg.ImageTools != nil { … }` block, add:

```go
	if tartBackend != nil && cfg.Runtime.Tart.Image != nil {
		homes := make(map[string]string, len(cfg.Runtime.Tart.Containers))
		for _, container := range cfg.Runtime.Tart.Containers {
			homes[container.ID] = container.Path
		}
		manager.SetImageBuilder(tartimage.New(tartimage.Config{
			StateDir:      cfg.StateDir,
			Packer:        cfg.Runtime.Tart.Image.Packer,
			XcodeArchives: cfg.Runtime.Tart.Image.XcodeArchives,
			Homes:         homes,
			Credentials:   tartCredentials,
			Runtime:       tartBackend,
		}))
	}
```

and import `"github.com/mallexxx/virfield/internal/tartimage"`.

- [x] **Step 2: Document the Tart window**

In `docs/OPERATIONS.md`, after the paragraph added by the guest-identity plan ("With `runtime.provider = "tart"` …"), add:

````markdown
### Tart image adoption

A Tart daemon uses its own `config.json` and `state_dir`; it never shares the
Lume journal. Switch the single launchd job only while no Lume lease is
active, and switch back the same way. Tart pulls from GHCR need
`runtime.tart.registry_credentials` for `ghcr.io`: Tart always reads
`~/.docker/config.json`, and a stale GHCR entry there is rejected.

```json
"runtime": {
  "provider": "tart",
  "tart": {
    "binary": "/opt/homebrew/bin/tart",
    "host_id": "local",
    "containers": [{"id": "samsung-tart", "path": "/Volumes/Samsung970EVO/virfield-tart", "kind": "external"}],
    "registry_credentials": [{"host": "ghcr.io", "username": "mallexxx", "token_file": "/Users/admin/.virfield-v2/registry-credentials/ghcr-mallexxx.token"}],
    "image": {"packer": "/opt/homebrew/bin/packer", "xcode_archives": "/Users/admin/.virfield-v2/cache/xcode"}
  }
}
```

A Tart template names the container as `location`, `guest_user: "admin"` and
an `image` with a `tart.upstream` digest, exact `macos`/`build`, `xcode` and
`developer-v1`. `image-build` then runs `resolve → build → qualify → stop`;
evidence is `images/<id>/verification.json` with the upstream and recipe
digests. Retry is available for `resolve` and `qualify`; an interrupted
`build` needs `delete` and a new build.
````

- [x] **Step 3: Verify everything**

Run: `make check >/tmp/virfield-check.log 2>&1; echo exit=$?; grep -E 'FAIL' /tmp/virfield-check.log | head`
Expected: `exit=0` and no `FAIL` lines.

- [x] **Step 4: Commit**

```bash
git add cmd/virfieldd/main.go docs/OPERATIONS.md
git commit -m "Wire Tart image adoption and document the Tart window"
```

---

## Self-review notes

- Spec coverage: Tart source + validation (T1); config (T2); `qualify` extraction with Lume using it (T3–T5); recipe with pinned plugin, OS/SIP/space fail-fast, XIP upload, Xcode install/post-install (T6); resolve/build/stop with scoped credentials and private logs (T7); qualify with secure → reboot → verify → evidence (T8); controller phases, collision check, retry rules (T9); wiring + operator docs (T10). Live acceptance, the Tart config and the Monterey build number are stage 3.
- Type consistency: `Builder{config, run, guests, deviceSupport}`, `Config{StateDir, Packer, XcodeArchives, Homes, Credentials, Runtime}`, `runner.Run(ctx, dir, env, log, name, args...)`, `guests.{Connect, Secure, Password}`, `session = qualify.Guest + Close`, `qualify.{OS, SIP, Password, Desktop, DiskPolicy, Xcode, XcodeBundle, DeviceSupport, WaitDesktop, DesktopProbe, Quote, AppleCodeRequirement}` are used identically across tasks.
- `domain.VM.IP` and `domain.VM.State` are existing fields (`internal/domain/model.go`).
