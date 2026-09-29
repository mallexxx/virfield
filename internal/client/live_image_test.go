package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// TestLiveImageRebuild exercises the deployed daemon, never Lume or its store.
// It PERMANENTLY DELETES the exactly named configured golden image, builds its
// replacement, and creates/deletes two disposable leases. Explicit operator
// authorization is mandatory. On ambiguity the daemon retains its journal and
// reservation; this test never uses operator recovery or retries mutations.
func TestLiveImageRebuild(t *testing.T) {
	if os.Getenv("VIRFIELD_LIVE_IMAGE_REBUILD") != "I_APPROVE_REBUILD_AND_TEMPORARY_VM_DELETION" {
		t.Skip("requires explicit authorization to delete/rebuild one golden image and two disposable clones")
	}
	id, name := os.Getenv("VIRFIELD_LIVE_TEMPLATE_ID"), os.Getenv("VIRFIELD_LIVE_TEMPLATE")
	if !domain.ValidName(id) || !domain.ValidName(name) {
		t.Fatal("set exact template ID and VM name")
	}
	token, err := os.ReadFile(os.Getenv("VIRFIELD_LIVE_TOKEN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := New("http://127.0.0.1:7780", strings.TrimSpace(string(token)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Minute)
	defer cancel()
	var maxRead time.Duration
	read := func(ctx context.Context, path string, target any) error {
		start := time.Now()
		b, err := c.Do(ctx, "GET", path, nil, "")
		maxRead = max(maxRead, time.Since(start))
		if err != nil {
			return err
		}
		return json.Unmarshal(b, target)
	}
	mutate := func(ctx context.Context, path string, body any, key string) (domain.Operation, error) {
		var op domain.Operation
		b, err := c.Do(ctx, "POST", path, body, key)
		if err == nil {
			err = json.Unmarshal(b, &op)
		}
		return op, err
	}
	waitJob := func(ctx context.Context, id string) error {
		previous := ""
		for {
			var j domain.Job
			if err := read(ctx, "jobs/"+id, &j); err != nil {
				return err
			}
			if j.Phase != previous {
				t.Logf("%s: %s (%s)", j.ID, j.Phase, j.State)
				previous = j.Phase
			}
			if j.State == "succeeded" {
				return nil
			}
			if j.State == "needs_attention" || j.State == "failed" || j.State == "canceled" {
				return errors.New("job requires inspection: " + j.ID + " " + j.State)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	var status domain.Status
	if err := read(ctx, "status", &status); err != nil {
		t.Fatal(err)
	}
	if status.Observation.Error != nil || time.Since(status.Observation.At) > 10*time.Second || status.Capacity.Used != 0 {
		t.Fatal("requires fresh healthy inventory and no occupied/reserved slots")
	}
	for _, vm := range status.Observation.VMs {
		if vm.State != "stopped" {
			t.Fatal("all existing VMs must be stopped; never stop unrelated VMs for this test")
		}
	}
	var template domain.Template
	for _, tm := range status.Templates {
		if tm.ID == id {
			template = tm
		}
	}
	if template.Name != name || template.Image == nil {
		t.Fatal("exact named template must have an image build profile")
	}
	prefix := domain.NewID("live-image-")
	present := false
	for _, vm := range status.Observation.VMs {
		if vm.Key() == template.Location+"/"+name {
			present = true
		}
	}
	if present {
		deleted, err := mutate(ctx, "images/"+id+"/delete", map[string]string{"confirm_name": name}, prefix+"-delete")
		if err != nil {
			t.Fatal(err)
		}
		if err := waitJob(ctx, deleted.Job.ID); err != nil {
			t.Fatal(err)
		}
	}
	built, err := mutate(ctx, "images/"+id+"/build", struct{}{}, prefix+"-build")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image record %s, build job %s", built.Lease.ID, built.Job.ID)
	if err := waitJob(ctx, built.Job.ID); err != nil {
		t.Fatal(err)
	}
	var image domain.Lease
	if err := read(ctx, "leases/"+built.Lease.ID, &image); err != nil {
		t.Fatal(err)
	}
	if image.State != "image_ready" || image.ImageManifest == "" {
		t.Fatal("image was not verified and promoted")
	}
	leases := []domain.Operation{}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 6*time.Minute)
		defer stop()
		for _, op := range leases {
			released, err := mutate(cleanup, "leases/"+op.Lease.ID+"/release", struct{}{}, prefix+"-release-"+op.Lease.ID)
			if err != nil {
				t.Error(err)
				continue
			}
			if err := waitJob(cleanup, released.Job.ID); err != nil {
				t.Error(err)
			}
		}
		if err := read(cleanup, "status", &status); err != nil {
			t.Error(err)
			return
		}
		for _, op := range leases {
			for _, vm := range status.Observation.VMs {
				if vm.Key() == op.Lease.Key() {
					t.Error("test VM still present", vm.Name)
				}
			}
		}
		found := false
		for _, vm := range status.Observation.VMs {
			if vm.Key() == image.Key() && vm.State == "stopped" {
				found = true
			}
		}
		if !found {
			t.Error("verified golden image must remain stopped")
		}
		t.Logf("maximum API read latency during live run: %s", maxRead)
	}()
	request := domain.AcquireRequest{Template: id, TTLSeconds: 3600}
	for _, suffix := range []string{"-first", "-second"} {
		op, err := mutate(ctx, "leases", request, prefix+suffix)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, op)
	}
	third, err := mutate(ctx, "leases", request, prefix+"-third")
	if err == nil {
		leases = append(leases, third)
		t.Fatal("third lease incorrectly admitted")
	}
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "capacity_exhausted" {
		t.Fatal(err)
	}
	t.Log(failure.Message)
	for _, op := range leases {
		if err := waitJob(ctx, op.Job.ID); err != nil {
			t.Fatal(err)
		}
	}
	replay, err := mutate(ctx, "leases", request, prefix+"-first")
	if err != nil || !replay.Replayed || replay.Lease.ID != leases[0].Lease.ID {
		t.Fatal("acquire idempotency failed", err)
	}
	for _, op := range leases {
		var l domain.Lease
		if err := read(ctx, "leases/"+op.Lease.ID, &l); err != nil || l.State != "ready" {
			t.Fatal("clone did not reach ready", err)
		}
	}
	t.Log("Fresh image build and promotion, two ready clones, third refusal and idempotency passed; cleanup follows.")
}
