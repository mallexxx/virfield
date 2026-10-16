package control

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type shutdownProcessBuilder struct {
	started chan *exec.Cmd
}

func (b shutdownProcessBuilder) Step(ctx context.Context, _ domain.Lease, _ domain.ImageProfile, _ string, _ func(string) error) error {
	cmd := exec.CommandContext(ctx, "sleep", "30")
	if err := cmd.Start(); err != nil {
		return err
	}
	b.started <- cmd
	return cmd.Wait()
}

func TestShutdownCancelsUnfinishedImageProcessAfterDrainGrace(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep unavailable")
	}
	c, _ := imageController(t)
	template := c.templates["test"]
	template.Image = &domain.ImageProfile{Tart: &domain.TartSource{Upstream: "ghcr.io/example/base@sha256:" + strings.Repeat("a", 64)}, MacOS: "12.7.6", Build: "21H1320", Provision: "base-v1"}
	c.templates["test"] = template
	c.shutdownGrace = 50 * time.Millisecond
	b := shutdownProcessBuilder{started: make(chan *exec.Cmd, 1)}
	c.SetImageBuilder(b)
	op, err := c.BuildImage(context.Background(), "test", "shutdown-process")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	var cmd *exec.Cmd
	select {
	case cmd = <-b.started:
	case <-time.After(3 * time.Second):
		t.Fatal("image process did not start")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("shutdown skipped the mutation grace period")
	case <-time.After(10 * time.Millisecond):
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		// Reap only this test's process even when the regression fails.
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("unfinished image process survived the shutdown grace")
	}
	if cmd.ProcessState == nil {
		t.Fatal("image process was not reaped")
	}
	job, err := c.Job(context.Background(), op.Job.ID)
	if err != nil || job.State != "needs_attention" || job.Phase != "resolve_dispatched" {
		t.Fatal("unknown image outcome was not retained for inspection", job, err)
	}
}
