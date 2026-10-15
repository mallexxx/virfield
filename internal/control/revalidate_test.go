package control

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/provider"
	"golang.org/x/crypto/ssh"
)

type readonlyRevalidator struct {
	started  chan domain.Lease
	finish   chan struct{}
	result   error
	calls    atomic.Int32
	prepares atomic.Int32
}

func (p *readonlyRevalidator) Prepare(context.Context, domain.Lease) (domain.SSHConnection, error) {
	p.prepares.Add(1)
	return domain.SSHConnection{}, errors.New("revalidation must never prepare a guest")
}
func (p *readonlyRevalidator) Revalidate(ctx context.Context, l domain.Lease) error {
	p.calls.Add(1)
	p.started <- l
	select {
	case <-p.finish:
		return p.result
	case <-ctx.Done():
		return ctx.Err()
	}
}

func setupRevalidation(t *testing.T) (*Controller, domain.Lease, *readonlyRevalidator, ssh.Signer) {
	t.Helper()
	c, l := readyVMLease(t, "revalidation-acquire")
	b := c.backend.(*fakeBackend)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	l.SSHPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	l.SSH.ClientKeyFingerprint = ssh.FingerprintSHA256(signer.PublicKey())
	l.SSH.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host.PublicKey())))
	l.Provider = provider.Name(c.backend)
	l.Owner = "broker"
	l.GuestHome = "/Users/admin"
	l.Resources = domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100 << 30}
	profile := domain.ImageProfile{URL: "https://updates.cdn-apple.com/test.ipsw", SHA256: strings.Repeat("0", 64), Size: 123, Build: "26A428"}
	template := *l.Source
	template.Image = &profile
	template.Provider = l.Provider
	template.GuestUser = l.GuestUser
	template.GuestHome = "/Users/admin"
	l.Source = &template
	c.templates[template.ID] = template
	image := domain.Lease{ID: l.ImageID, Purpose: "image", State: "image_ready", VMName: template.Name, Location: template.Location, Template: template.ID, ImageManifest: imageFingerprint(&profile)}
	if err := c.store.SaveImage(context.Background(), image, nil, "", "", "test.image", "Verified fixture image", template); err != nil {
		t.Fatal(err)
	}
	v := b.vms[l.Key()]
	v.Provider = l.Provider
	v.RuntimeID = "fake-owned:" + l.ID
	v.HostID = "host"
	v.ContainerID = l.Location
	v.Resources = l.Resources
	b.vms[l.Key()] = v
	baseline := runtimeIdentity(v, true, time.Now().UTC())
	l.VerifiedRuntime = &baseline
	if err := c.store.Save(context.Background(), l, nil, "", "", "test.identity", "Existing isolated fixture identity"); err != nil {
		t.Fatal(err)
	}
	p := &readonlyRevalidator{started: make(chan domain.Lease, 4), finish: make(chan struct{})}
	c.SetLeasePreparer(p)
	v.IP = "192.168.64.99"
	v.SSHPort = 2222
	b.vms[l.Key()] = v
	tick(t, c)
	tick(t, c)
	drifted, err := c.Lease(context.Background(), l.ID)
	if err != nil || drifted.State != "needs_attention" || drifted.Error.Code != "vm_drift" {
		t.Fatal(drifted, err)
	}
	return c, l, p, signer
}

func signRevalidation(t *testing.T, l domain.Lease, signer ssh.Signer, key string) domain.RevalidateRequest {
	t.Helper()
	signature, err := signer.Sign(rand.Reader, domain.RevalidationMessage(l.ID, l.VMName, key))
	if err != nil {
		t.Fatal(err)
	}
	return domain.RevalidateRequest{VMName: l.VMName, CallerProof: base64.StdEncoding.EncodeToString(ssh.Marshal(signature))}
}

func TestRevalidationLostReplyReplayPreservesVMIdentityKeysAndTTL(t *testing.T) {
	c, original, p, signer := setupRevalidation(t)
	b := c.backend.(*fakeBackend)
	counts := map[string]int{}
	for _, kind := range []string{"clone", "start", "stop", "delete"} {
		counts[kind] = b.count(kind)
	}
	request := signRevalidation(t, original, signer, "revalidation-first")
	op, err := c.RevalidateAs(context.Background(), original.ID, "revalidation-first", request, "broker")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := c.RevalidateAs(context.Background(), original.ID, "revalidation-first", request, "broker")
	if err != nil || !replay.Replayed || replay.Job.ID != op.Job.ID {
		t.Fatal(replay, err)
	}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var candidate domain.Lease
	select {
	case candidate = <-p.started:
	case <-time.After(time.Second):
		t.Fatal("Readonly verification did not start")
	}
	if candidate.ID != original.ID || candidate.VMName != original.VMName || candidate.IP != "192.168.64.99" || candidate.SSHPort != 2222 || !candidate.ExpiresAt.Equal(original.ExpiresAt) {
		t.Fatal(candidate)
	}
	close(p.finish)
	c.wg.Wait()
	l, err := c.Lease(context.Background(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := c.store.Job(context.Background(), op.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if l.State != "ready" || job.State != "succeeded" || l.IP != candidate.IP || l.SSH.Port != 2222 || l.SSH.HostKey != original.SSH.HostKey || l.SSH.ClientKeyFingerprint != original.SSH.ClientKeyFingerprint || l.SSHPublicKey != original.SSHPublicKey || !l.ExpiresAt.Equal(original.ExpiresAt) {
		t.Fatal(l, job)
	}
	if p.calls.Load() != 1 || p.prepares.Load() != 0 {
		t.Fatal("Replayed verification or credential provisioning", p.calls.Load(), p.prepares.Load())
	}
	for kind, before := range counts {
		if b.count(kind) != before {
			t.Fatal("Revalidation mutated VM lifecycle", kind)
		}
	}
	replay, err = c.RevalidateAs(context.Background(), original.ID, "revalidation-first", request, "broker")
	if err != nil || !replay.Replayed || replay.Job.ID != op.Job.ID {
		t.Fatal(replay, err)
	}
}

func TestRevalidationRejectsForeignProofReplacementExpiryAndMissingBaseline(t *testing.T) {
	for _, failure := range []string{"foreign-owner", "wrong-proof", "replacement", "expired", "missing-baseline", "image-changed"} {
		t.Run(failure, func(t *testing.T) {
			c, l, p, signer := setupRevalidation(t)
			key := "revalidation-denied"
			request := signRevalidation(t, l, signer, key)
			owner := ""
			expected := "guest_identity_failed"
			switch failure {
			case "foreign-owner":
				owner = "another-principal"
				expected = "forbidden"
			case "wrong-proof":
				request = signRevalidation(t, l, signer, "different-request")
				expected = "forbidden"
			case "replacement":
				b := c.backend.(*fakeBackend)
				v := b.vms[l.Key()]
				v.RuntimeID = "replacement"
				b.vms[l.Key()] = v
				tick(t, c)
			case "expired":
				c.now = func() time.Time { return l.ExpiresAt.Add(time.Second) }
				expected = "lease_expired"
			case "missing-baseline":
				stored, _ := c.store.Lease(context.Background(), l.ID)
				stored.VerifiedRuntime = nil
				if err := c.store.Save(context.Background(), stored, nil, "", "", "test.legacy", "Legacy fixture"); err != nil {
					t.Fatal(err)
				}
				expected = "revalidation_unsupported"
			case "image-changed":
				image, _ := c.store.Lease(context.Background(), l.ImageID)
				image.ImageManifest = "changed"
				if err := c.store.Save(context.Background(), image, nil, "", "", "test.imagechanged", "Changed fixture provenance"); err != nil {
					t.Fatal(err)
				}
			}
			_, err := c.RevalidateAs(context.Background(), l.ID, key, request, owner)
			code(t, err, expected)
			if p.calls.Load() != 0 || p.prepares.Load() != 0 {
				t.Fatal("Rejected request touched guest")
			}
		})
	}
}

func TestRevalidationDoesNotResurrectExpiredOrChangedLease(t *testing.T) {
	for _, failure := range []string{"expiry", "replacement", "auth-failed"} {
		t.Run(failure, func(t *testing.T) {
			c, l, p, signer := setupRevalidation(t)
			request := signRevalidation(t, l, signer, "revalidation-race")
			op, err := c.Revalidate(context.Background(), l.ID, "revalidation-race", request)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.started:
			case <-time.After(time.Second):
				t.Fatal("Revalidation not started")
			}
			c.mu.Lock()
			switch failure {
			case "expiry":
				c.now = func() time.Time { return l.ExpiresAt.Add(time.Second) }
			case "replacement":
				for i := range c.observation.VMs {
					if c.observation.VMs[i].Key() == l.Key() {
						c.observation.VMs[i].RuntimeID = "replacement"
					}
				}
			case "auth-failed":
				p.result = domain.Err("caller_authentication_failed", "Existing caller key is no longer admitted")
			}
			c.mu.Unlock()
			close(p.finish)
			c.wg.Wait()
			lease, err := c.Lease(context.Background(), l.ID)
			if err != nil {
				t.Fatal(err)
			}
			job, err := c.store.Job(context.Background(), op.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if lease.State == "ready" || job.State != "failed" || lease.ID != l.ID || lease.VMName != l.VMName || !lease.ExpiresAt.Equal(l.ExpiresAt) || p.prepares.Load() != 0 {
				t.Fatal(lease, job)
			}
		})
	}
}

func TestRevalidationRestartResumesSameReadonlyJobAndOriginalDeadline(t *testing.T) {
	c, l, originalValidator, signer := setupRevalidation(t)
	request := signRevalidation(t, l, signer, "revalidation-restart")
	op, err := c.Revalidate(context.Background(), l.ID, "revalidation-restart", request)
	if err != nil {
		t.Fatal(err)
	}
	job := op.Job
	job.State = "running"
	job.Phase = "verify_dispatched"
	if err := c.store.Save(context.Background(), op.Lease, &job, "", "", "test.interruption", "Simulated daemon interruption during read-only check"); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(c.store, c.backend, []domain.Template{c.templates[l.Template]}, c.limit, c.log)
	if err != nil {
		t.Fatal(err)
	}
	validator := &readonlyRevalidator{started: make(chan domain.Lease, 2), finish: make(chan struct{})}
	close(validator.finish)
	restarted.SetLeasePreparer(validator)
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	tick(t, restarted)
	completed, err := restarted.store.Job(context.Background(), op.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := restarted.Lease(context.Background(), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "succeeded" || !completed.Deadline.Equal(op.Job.Deadline) || lease.State != "ready" || !lease.ExpiresAt.Equal(l.ExpiresAt) || validator.calls.Load() != 1 || originalValidator.calls.Load() != 0 || validator.prepares.Load() != 0 {
		t.Fatal(lease, completed)
	}
	replayed, err := restarted.Revalidate(context.Background(), l.ID, "revalidation-restart", request)
	if err != nil || !replayed.Replayed || replayed.Job.ID != op.Job.ID {
		t.Fatal(replayed, err)
	}
}

func TestUnknownRuntimeMetricsPreserveReadyIdentityAndBlockNewTunnels(t *testing.T) {
	c, l, p, signer := setupRevalidation(t)
	close(p.finish)
	request := signRevalidation(t, l, signer, "revalidation-unknown")
	if _, err := c.Revalidate(context.Background(), l.ID, "revalidation-unknown", request); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	b := c.backend.(*fakeBackend)
	v := b.vms[l.Key()]
	v.Resources.DiskBytes = 0
	b.vms[l.Key()] = v
	for range 3 {
		tick(t, c)
	}
	lease, err := c.Lease(context.Background(), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.State != "ready" || lease.IP == "" || lease.VerifiedRuntime == nil {
		t.Fatal("Unknown size became verified drift", lease)
	}
	_, err = c.OpenTunnel(context.Background(), l.ID, "ssh")
	code(t, err, "backend_unavailable")
}

func TestRevalidationRestartNeverExtendsElapsedOriginalJobDeadline(t *testing.T) {
	c, l, _, signer := setupRevalidation(t)
	request := signRevalidation(t, l, signer, "revalidation-expired-restart")
	op, err := c.Revalidate(context.Background(), l.ID, "revalidation-expired-restart", request)
	if err != nil {
		t.Fatal(err)
	}
	job := op.Job
	job.State = "running"
	job.Phase = "verify_dispatched"
	if err := c.store.Save(context.Background(), op.Lease, &job, "", "", "test.interruption", "Read-only job interrupted"); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(c.store, c.backend, []domain.Template{c.templates[l.Template]}, c.limit, c.log)
	if err != nil {
		t.Fatal(err)
	}
	validator := &readonlyRevalidator{started: make(chan domain.Lease, 2), finish: make(chan struct{})}
	close(validator.finish)
	restarted.SetLeasePreparer(validator)
	restarted.now = func() time.Time { return op.Job.Deadline.Add(time.Second) }
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	tick(t, restarted)
	completed, err := restarted.store.Job(context.Background(), op.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "failed" || !completed.Deadline.Equal(op.Job.Deadline) || validator.calls.Load() != 0 {
		t.Fatal("Restart extended/replayed elapsed revalidation", completed)
	}
}

func TestLeaseExpiryCancelsInflightRevalidationWithoutResurrection(t *testing.T) {
	c, l, p, signer := setupRevalidation(t)
	b := c.backend.(*fakeBackend)
	stops, deletes := b.count("stop"), b.count("delete")
	request := signRevalidation(t, l, signer, "revalidation-expiry-cancel")
	op, err := c.Revalidate(context.Background(), l.ID, "revalidation-expiry-cancel", request)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("Revalidation not started")
	}
	c.mu.Lock()
	c.now = func() time.Time { return l.ExpiresAt.Add(time.Second) }
	c.mu.Unlock()
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.count("stop") != stops || b.count("delete") != deletes {
		t.Fatal("Cleanup dispatched before read-only verification drained")
	}
	close(p.finish)
	c.wg.Wait()
	lease, err := c.Lease(context.Background(), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := c.store.Job(context.Background(), op.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.State != "releasing" || job.State != "canceled" || !lease.ExpiresAt.Equal(l.ExpiresAt) || lease.SSH.HostKey != l.SSH.HostKey || p.prepares.Load() != 0 {
		t.Fatal("Expired lease resurrected after readonly result", lease, job)
	}
}

func TestSameEndpointRuntimeReplacementImmediatelyRevokesTunnel(t *testing.T) {
	c, l, p, signer := setupRevalidation(t)
	defer c.closeTunnels()
	close(p.finish)
	request := signRevalidation(t, l, signer, "revalidation-tunnel-identity")
	if _, err := c.Revalidate(context.Background(), l.ID, "revalidation-tunnel-identity", request); err != nil {
		t.Fatal(err)
	}
	tick(t, c)
	tunnel, err := c.OpenTunnel(context.Background(), l.ID, "ssh")
	if err != nil {
		t.Fatal(err)
	}
	b := c.backend.(*fakeBackend)
	v := b.vms[l.Key()]
	v.RuntimeID = "replacement-same-IP-port"
	b.vms[l.Key()] = v
	tick(t, c)
	if conn, err := net.DialTimeout("tcp", tunnel.Address, 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("Replacement inherited a listener at the same IP/port")
	}
	if _, err := c.OpenTunnel(context.Background(), l.ID, "ssh"); err == nil {
		t.Fatal("Replacement opened a new ready-lease tunnel")
	}
	waitDiagnostic(t, c, l.ID, "tunnel.listener", "verified_identity_change")
}
