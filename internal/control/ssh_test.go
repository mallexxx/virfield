package control

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

type blockingPreparer struct {
	started chan domain.Lease
	finish  chan struct{}
	calls   atomic.Int32
	err     error
}

func (p *blockingPreparer) Prepare(ctx context.Context, l domain.Lease) (domain.SSHConnection, error) {
	p.calls.Add(1)
	p.started <- l
	select {
	case <-p.finish:
	case <-ctx.Done():
		return domain.SSHConnection{}, ctx.Err()
	}
	return domain.SSHConnection{User: "lume", Port: 22, HostKey: l.SSHPublicKey, ClientKeyFingerprint: "test"}, p.err
}
func testPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(ssh.MarshalAuthorizedKey(key))
}
func sshController(t *testing.T) (*Controller, *blockingPreparer, domain.Operation) {
	t.Helper()
	c, _ := imageController(t)
	ctx := context.Background()
	if _, err := c.BuildImage(ctx, "test", "ssh-image-build"); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		tick(t, c)
	}
	p := &blockingPreparer{started: make(chan domain.Lease, 1), finish: make(chan struct{})}
	c.SetLeasePreparer(p)
	r := domain.AcquireRequest{Template: "test", TTLSeconds: 3600, SSHPublicKey: testPublicKey(t)}
	op, err := c.Acquire(ctx, "ssh-request-one", r)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := c.Acquire(ctx, "ssh-request-one", r)
	if err != nil || !replay.Replayed {
		t.Fatal(replay, err)
	}
	_, err = c.Acquire(ctx, "ssh-request-reuse", r)
	code(t, err, "ssh_key_in_use")
	tick(t, c)
	tick(t, c)
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("SSH worker not started")
	}
	return c, p, op
}
func TestSSHReadinessPreservesRenewal(t *testing.T) {
	c, p, op := sshController(t)
	ctx := context.Background()
	l, err := c.Lease(ctx, op.Lease.ID)
	if err != nil || l.State == "ready" || l.SSH != nil {
		t.Fatal("ready before authentication", err)
	}
	extended := op.Lease.ExpiresAt.Add(time.Hour)
	if _, err := c.Renew(ctx, l.ID, extended); err != nil {
		t.Fatal(err)
	}
	close(p.finish)
	c.wg.Wait()
	tick(t, c)
	l, err = c.Lease(ctx, l.ID)
	if err != nil || l.State != "ready" || l.SSH == nil || !l.ExpiresAt.Equal(extended) {
		t.Fatal("authentication or renewal lost", l.State, err)
	}
	if p.calls.Load() != 1 {
		t.Fatal("repeated SSH mutation")
	}
}
func TestSSHFailureAndRestartAllowCleanupWithoutRetry(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "restart"}[restart], func(t *testing.T) {
			c, p, op := sshController(t)
			p.err = domain.Err("injected_failure", "test failure")
			close(p.finish)
			c.wg.Wait()
			ctx := context.Background()
			if restart {
				l, _ := c.Lease(ctx, op.Lease.ID)
				j, _ := c.Job(ctx, op.Job.ID)
				l.State = "preparing"
				j.State = "running"
				j.Phase = "ssh_dispatched"
				if err := c.store.Save(ctx, l, &j, "", "", "test.restart", "test restart"); err != nil {
					t.Fatal(err)
				}
				if err := c.Recover(ctx); err != nil {
					t.Fatal(err)
				}
			}
			tick(t, c)
			l, err := c.Lease(ctx, op.Lease.ID)
			if err != nil || l.State != "needs_attention" || l.SSH != nil || p.calls.Load() != 1 {
				t.Fatal(l.State, err)
			}
			if _, err := c.Release(ctx, l.ID, "ssh-cleanup-request"); err != nil {
				t.Fatal(err)
			}
			for range 4 {
				tick(t, c)
			}
			l, err = c.Lease(ctx, l.ID)
			if err != nil || l.State != "released" {
				t.Fatal(l.State, err)
			}
		})
	}
}
func TestSSHExpiryDoesNotPublishReady(t *testing.T) {
	c, p, op := sshController(t)
	c.now = func() time.Time { return op.Lease.ExpiresAt.Add(time.Second) }
	close(p.finish)
	c.wg.Wait()
	tick(t, c)
	l, err := c.Lease(context.Background(), op.Lease.ID)
	if err != nil || l.State == "ready" {
		t.Fatal("expired lease became ready", err)
	}
}
