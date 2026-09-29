package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"golang.org/x/crypto/ssh"
)

// TestLiveLeaseSSH creates and deletes ONLY two disposable leases using the API.
// Guest probes are fixed commands; UI-test probes write only disposable guest
// artifacts. The golden image is preserved.
func TestLiveLeaseSSH(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_LEASE_SSH") != "I_APPROVE_TEMPORARY_VM_DELETION" {
		t.Skip("requires authorization for two temporary clones and cleanup")
	}
	token, err := os.ReadFile(os.Getenv("VIRFIELD_LIVE_TOKEN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := New("http://127.0.0.1:7780", strings.TrimSpace(string(token)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	read := func(ctx context.Context, path string, out any) error {
		b, err := c.Do(ctx, "GET", path, nil, "")
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	var status domain.Status
	if err := read(ctx, "status", &status); err != nil {
		t.Fatal(err)
	}
	if status.Capacity.Used != 0 || len(status.Jobs) != 0 {
		t.Fatal("requires idle v2 manager")
	}
	fullProfile := false
	for _, template := range status.Templates {
		if template.ID == os.Getenv("VIRFIELD_LIVE_TEMPLATE_ID") && template.Image != nil && template.Image.Provision == "uitest-27-v1" {
			fullProfile = true
		}
	}
	type owned struct {
		op      domain.Operation
		signer  ssh.Signer
		ready   domain.Lease
		request domain.AcquireRequest
		key     string
	}
	leases := []owned{}
	tunnels := map[string]string{}
	wait := func(ctx context.Context, id string) error {
		for {
			var j domain.Job
			if err := read(ctx, "jobs/"+id, &j); err != nil {
				return err
			}
			if j.State == "succeeded" {
				return nil
			}
			if j.State == "needs_attention" || j.State == "failed" {
				return j.Error
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		for _, l := range leases {
			b, err := c.Do(cleanup, "POST", "leases/"+l.op.Lease.ID+"/release", nil, l.key+"-release")
			if err != nil {
				t.Error("cleanup", l.op.Lease.ID, err)
				continue
			}
			var op domain.Operation
			if err := json.Unmarshal(b, &op); err != nil {
				t.Error(err)
				continue
			}
			if err := wait(cleanup, op.Job.ID); err != nil {
				t.Error("cleanup", l.op.Lease.ID, err)
			}
			if address := tunnels[l.op.Lease.ID]; address != "" {
				conn, err := net.DialTimeout("tcp", address, time.Second)
				if err == nil {
					conn.Close()
					t.Error("released tunnel still open")
				}
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("VIRFIELD_LIVE_STATE_DIR"), "leases", l.op.Lease.ID, "credentials.json")); !os.IsNotExist(err) {
				t.Error("released private credentials retained")
			}
		}
		if err := read(cleanup, "status", &status); err != nil {
			t.Error(err)
		} else if status.Capacity.Used != 0 {
			t.Error("slots retained after cleanup", status.Capacity.Used)
		}
		t.Log("Temporary lease cleanup completed")
	}()
	identityDirs := map[string]string{}
	fresh := func() (ssh.Signer, domain.AcquireRequest) {
		dir := filepath.Join(t.TempDir(), "identity")
		if err := GenerateIdentity(dir); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "id_ed25519"))
		if err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.ParsePrivateKey(b)
		if err != nil {
			t.Fatal(err)
		}
		identityDirs[ssh.FingerprintSHA256(signer.PublicKey())] = dir
		return signer, domain.AcquireRequest{Template: os.Getenv("VIRFIELD_LIVE_TEMPLATE_ID"), TTLSeconds: 900, SSHPublicKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))}
	}
	for range 2 {
		signer, r := fresh()
		key := domain.NewID("live-ssh-")
		b, err := c.Do(ctx, "POST", "leases", r, key)
		if err != nil {
			t.Fatal(err)
		}
		var op domain.Operation
		if err := json.Unmarshal(b, &op); err != nil {
			t.Fatal(err)
		}
		leases = append(leases, owned{op: op, signer: signer, request: r, key: key})
		t.Log("accepted", op.Lease.ID)
	}
	_, r := fresh()
	_, err = c.Do(ctx, "POST", "leases", r, domain.NewID("live-ssh-third-"))
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "capacity_exhausted" {
		t.Fatal("third lease did not report capacity_exhausted", err)
	}
	t.Log(failure.Message)
	for i := range leases {
		l := &leases[i]
		if err := wait(ctx, l.op.Job.ID); err != nil {
			t.Fatal(l.op.Lease.ID, err)
		}
		if err := read(ctx, "leases/"+l.op.Lease.ID, &l.ready); err != nil {
			t.Fatal(err)
		}
		if l.ready.State != "ready" || l.ready.SSH == nil {
			t.Fatal("missing verified connection")
		}
		b, err := c.Do(ctx, "POST", "leases", l.request, l.key)
		if err != nil {
			t.Fatal(err)
		}
		var replay domain.Operation
		if err := json.Unmarshal(b, &replay); err != nil || !replay.Replayed || replay.Lease.ID != l.ready.ID {
			t.Fatal("idempotency failed", err)
		}
	}
	// Optional operator-controlled hard restart: the harness never kills an external process.
	if marker := os.Getenv("VIRFIELD_LIVE_RESTART_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("two leases ready\n"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("ready for daemon restart; awaiting operator resume marker")
		restartCtx, stop := context.WithTimeout(ctx, 2*time.Minute)
		defer stop()
		for {
			if _, err := os.Stat(marker + ".resume"); err == nil {
				break
			}
			select {
			case <-restartCtx.Done():
				t.Fatal(restartCtx.Err())
			case <-time.After(time.Second):
			}
		}
		for _, l := range leases {
			var after domain.Lease
			if err := read(ctx, "leases/"+l.ready.ID, &after); err != nil {
				t.Fatal(err)
			}
			if after.State != "ready" || after.SSH == nil || *after.SSH != *l.ready.SSH {
				t.Fatal("SSH identity not preserved after restart")
			}
		}
		t.Log("daemon restart preserved ready leases and host pins")
	}
	if leases[0].ready.SSH.HostKey == leases[1].ready.SSH.HostKey {
		t.Fatal("clones share host identity")
	}
	dialAt := func(l domain.Lease, signer ssh.Signer, address string) (*ssh.Client, error) {
		host, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l.SSH.HostKey))
		if err != nil {
			return nil, err
		}
		raw, err := net.DialTimeout("tcp", address, 10*time.Second)
		if err != nil {
			return nil, err
		}
		_ = raw.SetDeadline(time.Now().Add(20 * time.Second))
		conn, ch, req, err := ssh.NewClientConn(raw, address, &ssh.ClientConfig{User: "lume", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, HostKeyCallback: ssh.FixedHostKey(host)})
		if err != nil {
			raw.Close()
			return nil, err
		}
		if fullProfile {
			_ = raw.SetDeadline(time.Now().Add(2 * time.Minute))
		}
		return ssh.NewClient(conn, ch, req), nil
	}
	dial := func(l domain.Lease, signer ssh.Signer) (*ssh.Client, error) {
		return dialAt(l, signer, net.JoinHostPort(l.IP, "22"))
	}
	deny := func(l domain.Lease, signer ssh.Signer) {
		t.Helper()
		g, err := dial(l, signer)
		if err == nil {
			g.Close()
			t.Fatal("foreign key authenticated")
		}
		if !strings.Contains(err.Error(), "unable to authenticate") {
			t.Fatal("refusal was not an authentication failure", err)
		}
	}
	for i, l := range leases {
		g, err := dial(l.ready, l.signer)
		if err != nil {
			t.Fatal("own key rejected", err)
		}
		s, err := g.NewSession()
		if err != nil {
			g.Close()
			t.Fatal(err)
		}
		out, err := s.CombinedOutput("/usr/sbin/sysctl -n hw.model; /usr/bin/csrutil status; /usr/bin/pgrep -x Finder; test -d ~/workspace")
		s.Close()
		if err != nil || !strings.Contains(string(out), "VirtualMac") || !strings.Contains(string(out), "disabled") {
			g.Close()
			t.Fatal("guest probes failed", err)
		}
		if fullProfile {
			s, err = g.NewSession()
			if err != nil {
				g.Close()
				t.Fatal(err)
			}
			out, err = s.CombinedOutput(`set -euo pipefail
export PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
xcodebuild -version
xcrun swift -e 'print("virfield-clone-swift-ok")'
sudo -n true
test "$(spctl --status 2>&1 || true)" = 'assessments disabled'
sysctl -n kern.bootargs | grep -q amfi_get_out_of_my_way=1
peekaboo permissions status --json --no-remote | jq -e '.success == true and ([.data.permissions[] | select(.isRequired == true)] | length >= 2 and all(.isGranted == true))'
osascript -e 'tell application "System Events" to get name of first process'
screencapture -x ~/workspace/virfield-acceptance.png
test -s ~/workspace/virfield-acceptance.png
printf 'virfield-ui-profile-ok\n'
`)
			s.Close()
			if err != nil || !strings.Contains(string(out), "virfield-ui-profile-ok") {
				g.Close()
				t.Fatal("cloned UI-test profile failed", err, string(out))
			}
			t.Log("cloned Xcode/Swift, security profile, UI permissions and screenshot passed", l.ready.ID)
		}
		g.Close()
		response, err := c.Do(ctx, "POST", "leases/"+l.ready.ID+"/tunnel", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		var tunnel domain.Tunnel
		if err := json.Unmarshal(response, &tunnel); err != nil {
			t.Fatal(err)
		}
		tunnels[l.ready.ID] = tunnel.Address
		tunneled, err := dialAt(l.ready, l.signer, tunnel.Address)
		if err != nil {
			t.Fatal("tunneled SSH authentication failed", err)
		}
		tunneled.Close()
		deny(l.ready, leases[1-i].signer)
		source, err := guestssh.LoadCredentials(filepath.Join(os.Getenv("VIRFIELD_LIVE_STATE_DIR"), "images", l.ready.ImageID, "credentials.json"))
		if err != nil {
			t.Fatal(err)
		}
		old, err := ssh.ParsePrivateKey(source.Private)
		if err != nil {
			t.Fatal(err)
		}
		deny(l.ready, old)
		scoped, err := guestssh.LoadCredentials(filepath.Join(os.Getenv("VIRFIELD_LIVE_STATE_DIR"), "leases", l.ready.ID, "credentials.json"))
		if err != nil {
			t.Fatal(err)
		}
		if scoped.Password == source.Password || string(scoped.Private) == string(source.Private) {
			t.Fatal("inherited image secret retained")
		}
		for _, path := range []string{"status", "events", "leases/" + l.ready.ID} {
			response, err := c.Do(ctx, "GET", path, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{scoped.Password, string(scoped.Private), string(scoped.HostPrivate), source.Password} {
				if len(secret) > 0 && strings.Contains(string(response), secret) {
					t.Fatal("private guest secret exposed by API", path)
				}
			}
		}
		dir := identityDirs[ssh.FingerprintSHA256(l.signer.PublicKey())]
		if err := WriteSSHConfig(dir, l.ready); err != nil {
			t.Fatal(err)
		}
		out, err = exec.CommandContext(ctx, "/usr/bin/ssh", "-F", filepath.Join(dir, "config"), "virfield", "/usr/sbin/sysctl -n hw.model").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "VirtualMac") {
			t.Fatal("exported OpenSSH configuration failed", err)
		}
		t.Log("own key authenticated; other lease and image keys rejected", l.ready.ID)
	}
	t.Log("PASS: two distinct authenticated host identities, cross-lease denial, image-key denial, Finder/SIP probes, capacity refusal and idempotency")
}
