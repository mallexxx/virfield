package guestssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestkeychain"
	"github.com/mallexxx/virfield/internal/tart"
	"golang.org/x/crypto/ssh"
)

// TestLiveDisposableGUIKeychain operates only on an explicitly approved fixture
// lease. The caller retains its API journal and releases that owned VM afterward.
func TestLiveDisposableGUIKeychain(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_KEYCHAIN") != "I_APPROVE_DISPOSABLE_VM_KEYCHAIN_FIX" {
		t.Skip("requires explicit disposable VM keychain approval")
	}
	var cfg struct {
		Listen    string `json:"listen"`
		StateDir  string `json:"state_dir"`
		TokenFile string `json:"token_file"`
	}
	b, err := os.ReadFile(os.Getenv("VIRFIELD_LIVE_CONFIG"))
	if err != nil || json.Unmarshal(b, &cfg) != nil || cfg.Listen != "127.0.0.1:7780" {
		t.Fatal("Expected the existing local deployment config", err)
	}
	token, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	id := os.Getenv("VIRFIELD_LIVE_KEYCHAIN_LEASE")
	if !domain.ValidName(id) || !strings.HasPrefix(id, "lease-") {
		t.Fatal("Expected fixture lease ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", "http://"+cfg.Listen+"/api/v1/leases/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var l domain.Lease
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&l) != nil || l.ID != id || l.Purpose == "image" || !strings.HasPrefix(l.Template, "release-check-") || !strings.HasPrefix(l.VMName, "vf-") {
		t.Fatal("Refusing to change a production lease or golden")
	}
	// An explicit fixture address must still authenticate against its stored pin.
	ip := l.IP
	if observed := os.Getenv("VIRFIELD_LIVE_KEYCHAIN_IP"); observed != "" {
		ip = observed
	}
	m := &Manager{Dir: cfg.StateDir, Scope: "leases"}
	g, err := m.ConnectReady(ctx, l, ip, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c, err := LoadCredentials(m.CredentialPath(l))
	if err != nil {
		t.Fatal(err)
	}

	proof, err := guestkeychain.Ensure(ctx, g, l.GuestUser, l.ID, c.Password)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := guestkeychain.Verify(ctx, g, l.GuestUser, l.ID)
	if err != nil || verified.Nonce == proof.Nonce || verified.SessionID != proof.SessionID {
		t.Fatal("Fresh non-unlocking GUI probe failed", err)
	}
	t.Logf("GUI WhenUnlocked add/read/delete passed: Aqua audit session %d, boot %s", verified.SessionID, verified.Boot)
	if os.Getenv("VIRFIELD_LIVE_KEYCHAIN_REBOOT") == "1" {
		_, _ = g.Run(ctx, "sudo -n /sbin/shutdown -r now", "")
		g.Close()
		time.Sleep(5 * time.Second)
		after, err := m.ConnectReady(ctx, l, ip, false)
		if err != nil {
			t.Fatal("Guest did not reconnect after the approved fixture reboot", err)
		}
		defer after.Close()
		rebooted, err := guestkeychain.Verify(ctx, after, l.GuestUser, l.ID)
		// Audit session numbers can be reused after a kernel reboot. The boot
		// UUID and fresh request nonce, rather than that number alone, bind it.
		if err != nil || rebooted.Boot == verified.Boot || rebooted.Nonce == verified.Nonce {
			t.Fatal("Automatic keychain readiness did not survive a real reboot", err)
		}
		t.Logf("Automatic Aqua unlock and fresh WhenUnlocked probe passed after reboot: audit session %d", rebooted.SessionID)
	}
}

// TestLiveIsolatedPublicBaseGUIKeychain accepts only a separately journaled
// disposable public-base VM outside the daemon's managed runtime containers.
func TestLiveIsolatedPublicBaseGUIKeychain(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_KEYCHAIN") != "I_APPROVE_DISPOSABLE_VM_KEYCHAIN_FIX" {
		t.Skip("requires disposable VM approval")
	}
	root := os.Getenv("VIRFIELD_LIVE_FIXTURE_DIR")
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		t.Fatal("VIRFIELD_LIVE_FIXTURE_DIR must name a private isolated fixture directory")
	}
	var journal struct{ IP, Home, VM, Upstream string }
	b, err := os.ReadFile(root + "/journal.json")
	if err != nil || json.Unmarshal(b, &journal) != nil || journal.Home != root+"/tart" || journal.VM != "release-check-keychain-macos27" || journal.Upstream != "ghcr.io/cirruslabs/macos-golden-gate-xcode@sha256:324ea5656dee8ab9b0a0df70fda2cfed8912051ad0eca3b1b883bf6ddac88fab" {
		t.Fatal("Expected own isolated public fixture journal", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	l := domain.Lease{ID: "lease-keychain-macos27-fixture", GuestUser: "admin"}
	m := &Manager{Dir: root + "/identity"}
	if err := os.MkdirAll(m.Directory(l), 0700); err != nil {
		t.Fatal(err)
	}
	g, err := m.ConnectReady(ctx, l, journal.IP, true)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c, err := LoadCredentials(m.CredentialPath(l))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("VIRFIELD_LIVE_KEYCHAIN_ROTATE_BASELINE") == "1" {
		if c.Password != "admin" {
			t.Fatal("Baseline rotation already completed")
		}
		c.Password = domain.NewID("vf-")
		if err := SaveCredentials(m.CredentialPath(l), c); err != nil {
			t.Fatal(err)
		}
		// Create an account/keychain password mismatch to exercise item-preserving migration.
		const passwordMismatch = `IFS= read -r user
IFS= read -r old
IFS= read -r new
/usr/sbin/sysadminctl -newPassword "$new" -oldPassword "$old"`
		if _, err := g.RunMutation(ctx, "/bin/bash -c '"+strings.ReplaceAll(passwordMismatch, "'", "'\"'\"'")+"'", "admin\nadmin\n"+c.Password+"\n"); err != nil {
			t.Fatal(err)
		}
		script := "set -eu\nIFS= read -r user\nIFS= read -r new\nIFS= read -r kcpassword\n" + autologinScript
		if _, err := g.RunMutation(ctx, "/bin/bash -c '"+strings.ReplaceAll(script, "'", "'\"'\"'")+"'", "admin\n"+c.Password+"\n"+loginPassword(c.Password)+"\n"); err != nil {
			t.Fatal(err)
		}
		_, _ = g.Run(ctx, "sudo -n /sbin/shutdown -r now", "")
		t.Log("Fixture account/keychain password mismatch created; reboot dispatched")
		return
	}
	if os.Getenv("VIRFIELD_LIVE_KEYCHAIN_BASELINE") == "1" {
		signer, err := ssh.ParsePrivateKey(c.Private)
		if err != nil {
			t.Fatal(err)
		}
		pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
		if _, err := g.Run(ctx, "umask 077; mkdir -p ~/.ssh; printf '%s\\n' '"+pub+"' >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys", ""); err != nil {
			t.Fatal(err)
		}
		c.Password = "admin"
		c.Secured = true
		if err := SaveCredentials(m.CredentialPath(l), c); err != nil {
			t.Fatal(err)
		}
		host, err := ssh.ParsePublicKey(c.HostKey)
		if err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"id_ed25519": c.Private, "known_hosts": []byte(journal.IP + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host))) + "\n")} {
			if err := os.WriteFile(root+"/"+name, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		config := "Host virfield\n HostName " + journal.IP + "\n User admin\n IdentityFile " + root + "/id_ed25519\n UserKnownHostsFile " + root + "/known_hosts\n StrictHostKeyChecking yes\n IdentitiesOnly yes\n BatchMode yes\n ConnectTimeout 45\n"
		if err := os.WriteFile(root+"/ssh-config", []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := g.Run(ctx, "sw_vers -productVersion; sw_vers -buildVersion", "")
		if err != nil || out != "27.0\n26A428\n" {
			t.Fatal("Wrong fixture OS", out, err)
		}
		t.Log("Isolated macOS 27 public fixture identity pinned; keychain left unchanged for baseline")
		return
	}
	proof, err := guestkeychain.Ensure(ctx, g, "admin", l.ID, c.Password)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := guestkeychain.Verify(ctx, g, "admin", l.ID)
	if err != nil || !verified.SSHVerified || verified.Nonce == proof.Nonce || verified.DisplayWidth != 1920 || verified.DisplayHeight != 1080 {
		t.Fatal("GUI/SSH verification", err)
	}
	t.Logf("macOS 27 native WhenUnlocked add/read/delete PASS: GUI session %d, SSH session %d", verified.SessionID, verified.SSHSessionID)
	_, _ = g.Run(ctx, "sudo -n /sbin/shutdown -r now", "")
	g.Close()
	time.Sleep(5 * time.Second)
	after, err := m.ConnectReady(ctx, l, journal.IP, false)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	rebooted, err := guestkeychain.Verify(ctx, after, "admin", l.ID)
	if err != nil || rebooted.Boot == verified.Boot || !rebooted.SSHVerified || rebooted.DisplayWidth != 1920 || rebooted.DisplayHeight != 1080 {
		t.Fatal("Post-reboot GUI/SSH keychain", err)
	}
	t.Logf("Real reboot PASS: new boot %s, native GUI and SSH probes passed", rebooted.Boot)
}

func TestLiveIsolatedFactoryLeaseReadiness(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_KEYCHAIN") != "I_APPROVE_DISPOSABLE_VM_KEYCHAIN_FIX" {
		t.Skip("requires disposable VM approval")
	}
	root := os.Getenv("VIRFIELD_LIVE_FIXTURE_DIR")
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		t.Fatal("VIRFIELD_LIVE_FIXTURE_DIR must name a private isolated fixture directory")
	}
	var factory struct {
		Lease domain.Lease
		Phase string
	}
	b, err := os.ReadFile(root + "/factory-journal.json")
	if err != nil || json.Unmarshal(b, &factory) != nil || factory.Phase != "qualified-stopped" || factory.Lease.VMName != "release-check-keychain-macos27-built" {
		t.Fatal("Expected own accepted isolated factory journal", err)
	}
	record := root + "/lease-journal.json"
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("Lease outcome already recorded; inspect before replay")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{ID: domain.NewID("lease-"), ImageID: factory.Lease.ID, Location: "isolated", GuestUser: "admin", GuestHome: "/Users/admin", SSHPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))}
	l.VMName = "vf-" + strings.TrimPrefix(l.ID, "lease-")
	save := func(phase string) {
		data, _ := json.MarshalIndent(map[string]any{"lease": l, "phase": phase}, "", "  ")
		if err := os.WriteFile(record, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := tart.New(tart.Config{Binary: "/opt/homebrew/bin/tart", HostID: "isolated-keychain-fixture", MaxVMs: 1, Containers: []tart.ContainerConfig{{ID: "isolated", Path: root + "/tart", Kind: "local"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	observation, err := runtime.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, vm := range observation.VMs {
		if vm.State == "running" {
			t.Fatal("Isolated factory must be stopped")
		}
	}
	template := domain.Template{Name: factory.Lease.VMName, Location: "isolated"}
	save("clone-dispatched")
	if err := runtime.Clone(ctx, template, l); err != nil {
		t.Fatal(err)
	}
	save("cloned")
	if err := runtime.Start(ctx, l); err != nil {
		t.Fatal(err)
	}
	save("started")
	for i := 0; i < 45; i++ {
		o, err := runtime.Observe(ctx)
		if err == nil {
			for _, vm := range o.VMs {
				if vm.Name == l.VMName {
					l.IP = vm.IP
				}
			}
		}
		if l.IP != "" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if l.IP == "" {
		t.Fatal("Owned clone IP unavailable")
	}
	save("prepare-dispatched")
	manager := &Manager{Dir: root + "/factory-state"}
	connection, err := manager.Prepare(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	l.SSH = &connection
	save("prepared")
	g, err := manager.derive("leases").ConnectReady(ctx, l, l.IP, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	proof, err := guestkeychain.Verify(ctx, g, "admin", l.ID)
	if err != nil || !proof.SSHVerified || proof.DisplayWidth != 1920 || proof.DisplayHeight != 1080 {
		t.Fatal("Source per-lease GUI/SSH/native display readiness", err)
	}
	credentials, err := LoadCredentials(manager.derive("leases").CredentialPath(l))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = g.Run(ctx, "sudo -n /sbin/shutdown -r now", "")
	g.Close()
	time.Sleep(5 * time.Second)
	g, err = manager.derive("leases").ConnectReady(ctx, l, l.IP, false)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	after, err := guestkeychain.Verify(ctx, g, "admin", l.ID)
	if err != nil || !after.SSHVerified || after.Boot == proof.Boot {
		t.Fatal("Prepared worker readiness did not survive reboot", err)
	}
	out, err := g.Run(ctx, "/usr/bin/stat -f %Lp ~/.virfield-keychain/identity; /usr/bin/wc -l < ~/.virfield-keychain/identity", "")
	if err != nil || strings.TrimSpace(out) != "600\n       2" && strings.Join(strings.Fields(out), " ") != "600 2" {
		t.Fatal("Inherited credential remained in worker or identity permissions changed", err)
	}
	if _, err := verifyCredentialsAuthProbe(ctx, g, credentials.Password); err != nil {
		t.Fatal(err)
	}
	_, _ = g.Run(ctx, "sudo -n /sbin/shutdown -h now", "")
	if err := runtime.Stop(ctx, l); err != nil {
		t.Fatal(err)
	}
	save("accepted-stopped")
	t.Log("Source Prepare PASS: caller/management/host identity isolation, private login rekey, native GUI/SSH and desktop readiness before/after real worker reboot")
}

func verifyCredentialsAuthProbe(ctx context.Context, g *Client, password string) (string, error) {
	return g.Run(ctx, "IFS= read -r password; /usr/bin/dscl . -authonly admin \"$password\" >/dev/null 2>&1", password+"\n")
}
