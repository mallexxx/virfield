# Tart Guest Identity and Lease SSH Isolation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `guestssh` use the template's guest account instead of the hardcoded `lume`, and enable per-lease SSH isolation for the Tart runtime.

**Architecture:** Stage 1 of [Tart image adoption](../specs/2026-10-07-tart-image-adoption-design.md). `domain.Lease.GuestUser` (already copied from the template at acquire time) becomes the single source of the guest account. Records without it are Lume records and default to `lume`, so Lume behavior is unchanged. Bootstrap images ship a password equal to the account name (Lume `lume/lume`, Cirrus `admin/admin`). Guest scripts receive the user on stdin like every other value. `virfieldd` wires `LeasePreparer` for every local runtime, so Tart leases become `ready` only after credential isolation.

**Tech Stack:** Go 1.x, `golang.org/x/crypto/ssh`, bash guest scripts. Tests: `go test`.

---

## File structure

| File | Change |
|---|---|
| `internal/guestssh/client.go` | `guestUser()` helper; bootstrap auth and SSH user from the lease; `rotateScript` constant with `$user` |
| `internal/guestssh/autologin.go` | `autologinScript` uses `$user`; `VerifyAutoLogin` takes the user |
| `internal/guestssh/lease.go` | `leaseScript` reads `$user`; image record inherits the lease user; returned `SSHConnection.User` |
| `internal/guestssh/client_test.go` | `sshServerFor` with configurable account; user tests |
| `internal/guestssh/autologin_test.go` | script parse and no-`lume` tests |
| `internal/control/image_build.go`, `internal/control/registry_publish.go` | image records carry the template guest user/home |
| `internal/control/image_build_test.go`, `internal/control/ssh_test.go` | propagation tests |
| `cmd/virfieldd/main.go` | wire `LeasePreparer` for all local runtimes |
| `docs/OPERATIONS.md` | Tart templates require a verified image profile |

---

### Task 1: SSH account comes from the lease

**Files:**
- Modify: `internal/guestssh/client.go` (`connectCredentials`)
- Test: `internal/guestssh/client_test.go`

- [ ] **Step 1: Make the fake SSH server's account configurable**

In `internal/guestssh/client_test.go`, rename `sshServer` to `sshServerFor` with a `user` parameter, and keep `sshServer` as the Lume wrapper:

```go
func sshServer(t *testing.T, model string) (string, ssh.Signer) {
	return sshServerFor(t, model, "lume")
}

func sshServerFor(t *testing.T, model, user string) (string, ssh.Signer) {
	t.Helper()
	// ... existing body unchanged, except the password callback:
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if c.User() != user || string(password) != user {
			return nil, io.EOF
		}
		return nil, nil
	}}
	// ...
}
```

- [ ] **Step 2: Write the failing tests**

Append to `internal/guestssh/client_test.go`:

```go
func TestBootstrapUsesTemplateGuestUser(t *testing.T) {
	port, _ := sshServerFor(t, "VirtualMac2,1", "admin")
	e, l := testImageGuest(t, port)
	l.GuestUser = "admin"
	g, err := e.Connect(context.Background(), l, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
}

func TestRecordsWithoutGuestUserRemainLume(t *testing.T) {
	port, _ := sshServerFor(t, "VirtualMac2,1", "admin")
	e, l := testImageGuest(t, port)
	if _, err := e.Connect(context.Background(), l, "127.0.0.1", true); err == nil {
		t.Fatal("record without guest user authenticated as admin")
	}
	if guestUser(domain.Lease{}) != "lume" || guestUser(domain.Lease{GuestUser: "admin"}) != "admin" {
		t.Fatal("unexpected guest user default")
	}
}

func TestInvalidGuestUserIsRefused(t *testing.T) {
	e, l := testImageGuest(t, "22")
	l.GuestUser = "admin'; rm -rf /"
	if _, err := e.Connect(context.Background(), l, "127.0.0.1", true); err == nil {
		t.Fatal("accepted invalid guest user")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/guestssh/ -run 'TestBootstrapUsesTemplateGuestUser|TestRecordsWithoutGuestUserRemainLume|TestInvalidGuestUserIsRefused' -v`
Expected: FAIL to compile with `undefined: guestUser`.

- [ ] **Step 4: Implement**

In `internal/guestssh/client.go`, add above `connectCredentials`:

```go
// guestUser is the image account a lease or image record was created for.
// Records written before guest users were configurable are Lume images.
func guestUser(l domain.Lease) string {
	if l.GuestUser == "" {
		return "lume"
	}
	return l.GuestUser
}
```

Replace the start of `connectCredentials` through the `cfg :=` line with:

```go
	user := guestUser(l)
	if !domain.ValidName(user) {
		return nil, domain.Err("guest_identity_failed", "Guest user name is invalid")
	}
	signer, err := ssh.ParsePrivateKey(c.Private)
	if err != nil {
		return nil, err
	}
	auth := []ssh.AuthMethod{ssh.PublicKeys(signer)}
	// Bootstrap is allowed only inside the new-image pipeline, never as a
	// connection fallback for arbitrary templates or leased execution VMs.
	// Supported images ship a bootstrap password equal to the account name:
	// Lume lume/lume and Cirrus Tart admin/admin.
	if bootstrap && !c.Secured {
		auth = append(auth, ssh.Password(user))
	}
	cfg := &ssh.ClientConfig{User: user, Auth: auth, Timeout: 10 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
```

(the rest of the `HostKeyCallback` body and the function are unchanged).

- [ ] **Step 5: Run the package tests**

Run: `go test ./internal/guestssh/ -v`
Expected: PASS, including the existing `TestGuestPinsKeyBoundsOutputAndCancels`.

- [ ] **Step 6: Commit**

```bash
git add internal/guestssh/client.go internal/guestssh/client_test.go
git commit -m "Connect to guests as the template guest user"
```

---

### Task 2: Guest scripts read the account from stdin

**Files:**
- Modify: `internal/guestssh/client.go` (`SecureImage`), `internal/guestssh/autologin.go`, `internal/guestssh/lease.go`
- Test: `internal/guestssh/autologin_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/guestssh/autologin_test.go`, replace `TestCredentialScriptsParse` with:

```go
func TestCredentialScriptsParse(t *testing.T) {
	for _, script := range []string{autologinScript, leaseScript, rotateScript} {
		cmd := exec.Command("/bin/bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid credential script: %s: %v", out, err)
		}
	}
}

func TestCredentialScriptsUseConfiguredGuestUser(t *testing.T) {
	for name, script := range map[string]string{"autologin": autologinScript, "lease": leaseScript, "rotate": rotateScript} {
		if strings.Contains(script, "lume") || !strings.Contains(script, `"$user"`) {
			t.Fatalf("%s script does not use the configured guest user", name)
		}
	}
	for name, script := range map[string]string{"lease": leaseScript, "rotate": rotateScript} {
		if !strings.HasPrefix(strings.TrimPrefix(script, "set -eu\n"), "IFS= read -r user\n") {
			t.Fatalf("%s script must read the guest user first", name)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/guestssh/ -run 'TestCredentialScripts' -v`
Expected: FAIL to compile with `undefined: rotateScript`.

- [ ] **Step 3: Implement `autologin.go`**

Change the last line of `autologinScript` so it reads:

```go
printf '%s\n' "$new" | sudo -S -p '' /usr/bin/defaults write /Library/Preferences/com.apple.loginwindow autoLoginUser -string "$user"
```

Replace `VerifyAutoLogin` and `BootstrapAutoLogin` with:

```go
// VerifyAutoLogin reads the private cache over pinned SSH and never returns its
// contents. sysadminctl can exit successfully without changing the stored secret.
func VerifyAutoLogin(ctx context.Context, g *Client, user, password string) error {
	failure := domain.Err("credential_autologin_failed", "Guest automatic login does not contain the current private password; image or lease will not be published")
	// ValidName admits no quote or shell metacharacter.
	if !domain.ValidName(user) {
		return failure
	}
	out, err := g.Run(ctx, `test "$(/usr/bin/defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser)" = '`+user+`' && sudo -S -p '' /bin/sh -c 'test "$(/usr/bin/stat -f %u:%g:%Lp /etc/kcpassword)" = 0:0:600 && /usr/bin/base64 -i /etc/kcpassword'`, password+"\n")
	if err != nil || !matchesLoginPassword(out, password) {
		return failure
	}
	return nil
}

// BootstrapAutoLogin is confined to a fresh Lume guest's native Setup Assistant.
// Credential rotation remains in the subsequent journaled assistant stage.
func BootstrapAutoLogin(ctx context.Context, g *Client) error {
	script := `set -eu
IFS= read -r user
IFS= read -r new
IFS= read -r kcpassword
umask 077
mkdir -p ~/.ssh
chmod 700 ~/.ssh
` + autologinScript
	_, err := g.Run(ctx, "/bin/bash -c '"+strings.ReplaceAll(script, "'", "'\"'\"'")+"'", "lume\nlume\n"+loginPassword("lume")+"\n")
	if err != nil {
		return err
	}
	return VerifyAutoLogin(ctx, g, "lume", "lume")
}
```

- [ ] **Step 4: Implement `client.go` (`SecureImage`)**

Add next to `guestUser`:

```go
// Fixed script; user, old and new password arrive on SSH stdin.
const rotateScript = `IFS= read -r user
IFS= read -r old
IFS= read -r new
if ! /usr/bin/dscl . -authonly "$user" "$new" >/dev/null 2>&1; then
 /usr/sbin/sysadminctl -newPassword "$new" -oldPassword "$old" || exit 1
fi
/usr/bin/dscl . -authonly "$user" "$new" >/dev/null 2>&1
`
```

In `SecureImage`, replace the block from `old := "lume"` through the `VerifyAutoLogin` call with:

```go
	user := guestUser(l)
	old := user
	if c.Secured {
		old = c.Password
	}
	if err := runSecret("rotation", rotateScript, user+"\n"+old+"\n"+c.Password+"\n"); err != nil {
		return err
	}
	autologin := `set -eu
IFS= read -r user
IFS= read -r new
IFS= read -r kcpassword
` + autologinScript
	if err := runSecret("autologin", autologin, user+"\n"+c.Password+"\n"+loginPassword(c.Password)+"\n"); err != nil {
		return err
	}
	if err := VerifyAutoLogin(ctx, g, user, c.Password); err != nil {
		return err
	}
```

- [ ] **Step 5: Implement `lease.go`**

In `leaseScript`, insert `IFS= read -r user` as the first line after `set -eu`, and change the `dscl` line to:

```go
/usr/bin/dscl . -authonly "$user" "$new" >/dev/null 2>&1
```

In `Prepare`:

```go
	image := domain.Lease{ID: l.ImageID, GuestUser: l.GuestUser}
```

```go
	user := guestUser(l)
	input := strings.Join([]string{user, inherited.Password, c.Password, management, public, base64.StdEncoding.EncodeToString(c.HostPrivate), loginPassword(c.Password)}, "\n") + "\n"
```

```go
	if err := VerifyAutoLogin(ctx, verified, user, c.Password); err != nil {
```

```go
	return domain.SSHConnection{User: user, Port: 22, HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host))), ClientKeyFingerprint: ssh.FingerprintSHA256(clientKey)}, nil
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/guestssh/ ./internal/images/ -v 2>&1 | tail -30`
Expected: PASS. `internal/images` still compiles because `BootstrapAutoLogin`'s signature is unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/guestssh/
git commit -m "Pass the guest user to credential scripts"
```

---

### Task 3: Image and lease records carry the guest user

**Files:**
- Modify: `internal/control/image_build.go:58`, `internal/control/registry_publish.go:109`
- Test: `internal/control/image_build_test.go`, `internal/control/ssh_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/control/image_build_test.go`:

```go
func TestImageRecordCarriesTemplateGuestUser(t *testing.T) {
	c, _ := imageController(t)
	tmpl := c.templates["test"]
	tmpl.GuestUser, tmpl.GuestHome = "admin", "/Users/admin"
	c.templates["test"] = tmpl
	op, err := c.BuildImage(context.Background(), "test", "image-guest-user")
	if err != nil {
		t.Fatal(err)
	}
	if op.Lease.GuestUser != "admin" || op.Lease.GuestHome != "/Users/admin" {
		t.Fatalf("image record guest = %q %q", op.Lease.GuestUser, op.Lease.GuestHome)
	}
}
```

Append to `internal/control/ssh_test.go`:

```go
func TestPreparedLeaseCarriesTemplateGuestUser(t *testing.T) {
	c, _ := imageController(t)
	tmpl := c.templates["test"]
	tmpl.GuestUser, tmpl.GuestHome = "admin", "/Users/admin"
	c.templates["test"] = tmpl
	ctx := context.Background()
	if _, err := c.BuildImage(ctx, "test", "guest-user-build"); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		tick(t, c)
	}
	p := &blockingPreparer{started: make(chan domain.Lease, 1), finish: make(chan struct{})}
	c.SetLeasePreparer(p)
	r := domain.AcquireRequest{Template: "test", TTLSeconds: 3600, SSHPublicKey: testPublicKey(t)}
	if _, err := c.Acquire(ctx, "guest-user-acquire", r); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	tick(t, c)
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case l := <-p.started:
		if l.GuestUser != "admin" {
			t.Fatalf("prepared lease guest user = %q", l.GuestUser)
		}
	case <-time.After(time.Second):
		t.Fatal("SSH worker not started")
	}
	close(p.finish)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/control/ -run 'TestImageRecordCarriesTemplateGuestUser|TestPreparedLeaseCarriesTemplateGuestUser' -v`
Expected: `TestImageRecordCarriesTemplateGuestUser` FAILS with `image record guest = "" ""`. `TestPreparedLeaseCarriesTemplateGuestUser` should already PASS (acquire copies the user at `controller.go:400`); it guards that behavior.

- [ ] **Step 3: Implement**

`internal/control/image_build.go:58`, add `GuestUser: t.GuestUser, GuestHome: t.GuestHome` to the image record:

```go
	l := domain.Lease{ID: domain.NewID("image-"), Purpose: "image", Template: t.ID, VMName: t.Name, Location: t.Location, LegacyUUID: t.LegacyUUID, GuestUser: t.GuestUser, GuestHome: t.GuestHome, State: "image_building", CreatedAt: now, UpdatedAt: now}
```

`internal/control/registry_publish.go:109`, same addition:

```go
	l := domain.Lease{ID: id, Purpose: "image", Portable: true, Template: t.ID, VMName: "vf-export-" + id[6:], Location: t.Location, GuestUser: t.GuestUser, GuestHome: t.GuestHome, State: "image_building", Source: &t, CreatedAt: now, UpdatedAt: now}
```

- [ ] **Step 4: Run the control tests**

Run: `go test ./internal/control/ 2>&1 | tail -5`
Expected: `ok  github.com/mallexxx/virfield/internal/control`

- [ ] **Step 5: Commit**

```bash
git add internal/control/
git commit -m "Record the template guest user on image builds"
```

---

### Task 4: Enable lease SSH isolation for Tart

**Files:**
- Modify: `cmd/virfieldd/main.go:196-198`, `docs/OPERATIONS.md`

- [ ] **Step 1: Implement**

Replace

```go
	if lumeBackend != nil {
		manager.SetLeasePreparer(&guestssh.Manager{Dir: cfg.StateDir})
	}
```

with

```go
	// Every local runtime hands out leases only after per-lease credential
	// isolation; Orchard leases are isolated by the Orchard worker path later.
	if !remoteRuntime {
		manager.SetLeasePreparer(&guestssh.Manager{Dir: cfg.StateDir})
	}
```

- [ ] **Step 2: Document the consequence**

In `docs/OPERATIONS.md`, after the import paragraph that ends at line 70 with "The pinned Lume decoder is not an untrusted-input sandbox.", add a new paragraph:

```markdown
With `runtime.provider = "tart"`, leases use the same per-lease SSH isolation
as Lume. A Tart template therefore needs a verified image profile and every
acquire needs a fresh `ssh_public_key`; a bare digest-pinned `source` template
without a qualified image record is rejected with `ssh_profile_missing`.
```

- [ ] **Step 3: Verify**

Run: `make check 2>&1 | tail -5; echo exit=$?`
Expected: last lines show `ok` packages and `exit=0`.

- [ ] **Step 4: Commit**

```bash
git add cmd/virfieldd/main.go docs/OPERATIONS.md
git commit -m "Isolate Tart lease SSH credentials"
```

---

## Self-review notes

- Spec coverage (stage 1): guest user parameterization in `guestssh` → Tasks 1–2; image/lease records → Task 3; `LeasePreparer` for Tart → Task 4. The `internal/images` Lume pipeline keeps its literal `lume` on purpose (it is not extended; stage 2 extracts the reusable checks into `internal/qualify`).
- Lume is unchanged in behavior: records without `GuestUser` default to `lume`, and new Lume image records get `lume` from the template default (`controller.go:95-100`).
