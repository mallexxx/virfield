// Package control implements admission, leases and recovery. One controller owns
// one state directory, protected by the daemon's process lock. The mutex protects
// both admission and transitions; no network operation runs while holding it.
package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/store"
)

type Backend interface {
	Observe(context.Context) (domain.Observation, error)
	Clone(context.Context, domain.Template, domain.Lease) error
	Start(context.Context, domain.Lease) error
	Stop(context.Context, domain.Lease) error
	Delete(context.Context, domain.Lease) error
}
type Controller struct {
	imageBuilder  ImageBuilder
	leasePreparer LeasePreparer
	mu            sync.Mutex
	store         *store.Store
	backend       Backend
	templates     map[string]domain.Template
	observation   domain.Observation
	limit         int
	now           func() time.Time
	active        map[string]bool
	wg            sync.WaitGroup
	log           *slog.Logger
}

func New(s *store.Store, b Backend, templates []domain.Template, limit int, log *slog.Logger) (*Controller, error) {
	if limit < 1 || limit > domain.MaxVMs {
		return nil, fmt.Errorf("max_vms must be 1 or 2")
	}
	tm := map[string]domain.Template{}
	identities := map[string]bool{}
	for _, t := range templates {
		if !domain.ValidName(t.ID) || !domain.ValidName(t.Name) || !domain.ValidName(t.Location) {
			return nil, fmt.Errorf("invalid template identity")
		}
		identity := t.Location + "/" + t.Name
		if identities[identity] {
			return nil, fmt.Errorf("duplicate template VM identity %s", identity)
		}
		identities[identity] = true
		if _, ok := tm[t.ID]; ok {
			return nil, fmt.Errorf("duplicate template %s", t.ID)
		}
		if t.Image != nil {
			if err := t.Image.Validate(); err != nil {
				return nil, err
			}
		}
		tm[t.ID] = t
	}
	return &Controller{store: s, backend: b, templates: tm, limit: limit, now: func() time.Time { return time.Now().UTC() }, active: map[string]bool{}, log: log, observation: domain.Observation{Error: domain.Err("backend_unavailable", "Lume inventory has not been checked yet")}}, nil
}
func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func validKey(key string) error {
	if len(key) < 8 || len(key) > 128 {
		return domain.Err("invalid_request", "Idempotency-Key must contain 8–128 printable ASCII characters")
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return domain.Err("invalid_request", "invalid Idempotency-Key")
		}
	}
	return nil
}
func (c *Controller) healthy() error {
	if c.observation.Error != nil || c.now().Sub(c.observation.At) > 10*time.Second {
		return &domain.Error{Code: "backend_unavailable", Message: "Lume state is unavailable or stale. New VMs are blocked until inventory recovers.", Retryable: true}
	}
	return nil
}
func (c *Controller) capacity(leases []domain.Lease) domain.Capacity {
	// Union observed active VMs with durable reservations, not sum (no double count).
	used := map[string]bool{}
	observed := 0
	for _, v := range c.observation.VMs {
		if v.State != "stopped" {
			used[v.Key()] = true
			observed++
		}
	}
	for _, l := range leases {
		if l.State != "released" && l.State != "image_ready" {
			used[l.Key()] = true
		}
	}
	unknown := max(0, c.observation.HostUsed-observed)
	names := make([]string, 0, len(used))
	for k := range used {
		names = append(names, k)
	}
	sort.Strings(names)
	if unknown > 0 {
		names = append(names, fmt.Sprintf("%d additional slot(s) reported by Lume", unknown))
	}
	n := len(used) + unknown
	limit := min(c.limit, c.observation.HostMax)
	if limit < 1 {
		limit = c.limit
	}
	available := max(0, limit-n)
	for _, l := range leases {
		if l.Purpose == "image" && l.State != "image_ready" && l.State != "released" {
			available = 0
			names = append(names, "Image operation reserves maintenance access; new leases paused")
			break
		}
	}
	return domain.Capacity{Limit: limit, Used: n, Available: available, Blockers: names}
}
func (c *Controller) Status(ctx context.Context) (domain.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return domain.Status{}, err
	}
	js, err := c.store.Jobs(ctx)
	if err != nil {
		return domain.Status{}, err
	}
	ts := make([]domain.Template, 0, len(c.templates))
	for _, t := range c.templates {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
	o := c.observation
	capacity := c.capacity(ls)
	if err := c.healthy(); err != nil {
		o.Error = err.(*domain.Error)
		capacity.Available = 0
		capacity.Blockers = append(capacity.Blockers, "Lume inventory is unverified; admission paused")
	}
	return domain.Status{Capacity: capacity, Observation: o, Leases: ls, Jobs: js, Templates: ts}, nil
}
func (c *Controller) Acquire(ctx context.Context, key string, r domain.AcquireRequest) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	if r.SSHPublicKey != "" {
		canonical, err := domain.CanonicalPublicKey(r.SSHPublicKey)
		if err != nil {
			return domain.Operation{}, err
		}
		r.SSHPublicKey = canonical
	}
	if err := r.Validate(); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fp := fingerprint(struct {
		Kind    string
		Request domain.AcquireRequest
	}{"acquire", r})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	t, ok := c.templates[r.Template]
	if !ok {
		return domain.Operation{}, domain.Err("unknown_template", "template is not in the host allowlist")
	}
	found := false
	for _, v := range c.observation.VMs {
		if v.Key() == t.Location+"/"+t.Name {
			found = true
			if v.State != "stopped" || v.OS != "macOS" {
				return domain.Operation{}, domain.Err("template_unavailable", "golden template must be a stopped macOS VM")
			}
		}
	}
	if !found {
		return domain.Operation{}, domain.Err("template_unavailable", "configured golden template is missing from Lume")
	}
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	for _, l := range ls {
		if l.Purpose == "image" && l.State != "image_ready" {
			return domain.Operation{}, domain.Err("image_in_use", "image operation is in progress")
		}
	}
	imageID := ""
	if c.leasePreparer != nil && (t.Image == nil || r.SSHPublicKey == "") {
		return domain.Operation{}, domain.Err("ssh_profile_missing", "A verified image profile and a fresh ssh_public_key are required for each lease")
	}
	for _, active := range ls {
		if r.SSHPublicKey != "" && active.SSHPublicKey == r.SSHPublicKey {
			return domain.Operation{}, domain.Err("ssh_key_in_use", "Use a distinct client SSH key for each active lease")
		}
	}
	if t.Image != nil {
		verified := false
		for _, l := range ls {
			if l.Purpose == "image" && l.Key() == t.Location+"/"+t.Name && l.State == "image_ready" && l.ImageManifest == fingerprint(t.Image) {
				verified = true
				imageID = l.ID
			}
		}
		if !verified {
			return domain.Operation{}, domain.Err("template_unavailable", "Configured image profile has no matching verified build; build and verify it before acquiring leases")
		}
	}
	cap := c.capacity(ls)
	if cap.Available == 0 {
		return domain.Operation{}, cap.FullError()
	}
	now := c.now()
	id := domain.NewID("lease-")
	l := domain.Lease{Source: &t, ImageID: imageID, SSHPublicKey: r.SSHPublicKey, ID: id, VMName: "vf-" + id[6:], Location: t.Location, Template: t.ID, State: "pending", ExpiresAt: now.Add(time.Duration(r.TTLSeconds) * time.Second), CreatedAt: now, UpdatedAt: now}
	j := domain.Job{ID: domain.NewID("job-"), LeaseID: id, Kind: "prepare", Phase: "queued", State: "queued", CreatedAt: now, UpdatedAt: now, Deadline: now.Add(10 * time.Minute)}
	if err := c.store.Save(ctx, l, &j, key, fp, "lease.accepted", "Slot reserved; VM preparation queued"); err != nil {
		return domain.Operation{}, err
	}
	return domain.Operation{Lease: l, Job: j}, nil
}
func (c *Controller) Release(ctx context.Context, id, key string) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fp := fingerprint([]string{"release", id})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	l, err := c.store.Lease(ctx, id)
	if err != nil {
		return domain.Operation{}, err
	}
	if l.Purpose == "image" {
		return domain.Operation{}, domain.Err("invalid_request", "use image operations for permanent images")
	}
	if l.State == "released" {
		return domain.Operation{}, domain.Err("lease_released", "lease is already released")
	}
	if c.active[id] {
		return domain.Operation{}, &domain.Error{Code: "operation_in_progress", Message: "A Lume operation is still in flight. Retry release after its result is recorded.", Retryable: true}
	}
	jobs, err := c.store.Jobs(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	for _, j := range jobs {
		if j.LeaseID == id && j.Kind == "cleanup" {
			return domain.Operation{}, domain.Err("operation_in_progress", "cleanup already exists; inspect its job")
		}
	}
	if l.State == "quarantined" {
		return domain.Operation{}, domain.Err("outcome_unknown", "A clone may still be in progress. Inspect Lume before resolving this lease; automatic deletion is blocked.")
	}
	return c.queueCleanup(ctx, l, key, fp)
}
func (c *Controller) queueCleanup(ctx context.Context, l domain.Lease, key, fp string) (domain.Operation, error) {
	now := c.now()
	l.State = "releasing"
	l.UpdatedAt = now
	l.Error = nil
	j := domain.Job{ID: domain.NewID("job-"), LeaseID: l.ID, Kind: "cleanup", Phase: "queued", State: "queued", CreatedAt: now, UpdatedAt: now, Deadline: now.Add(5 * time.Minute)}
	err := c.store.Save(ctx, l, &j, key, fp, "cleanup.queued", "Lease cleanup queued; slot remains reserved")
	return domain.Operation{Lease: l, Job: j}, err
}
func (c *Controller) Renew(ctx context.Context, id string, expires time.Time) (domain.Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.store.Lease(ctx, id)
	if err != nil {
		return l, err
	}
	if l.Purpose == "image" {
		return l, domain.Err("invalid_request", "use image operations for permanent images")
	}
	now := c.now()
	if !l.ExpiresAt.After(now) || l.State == "released" || l.State == "releasing" || l.State == "quarantined" {
		return l, domain.Err("lease_expired", "lease cannot be renewed")
	}
	if !expires.After(now) || expires.After(now.Add(24*time.Hour)) {
		return l, domain.Err("invalid_request", "expires_at must be in the next 24 hours")
	}
	// Absolute deadline makes renew replay-safe. Older renewals cannot shorten a lease.
	if !expires.After(l.ExpiresAt) {
		return l, nil
	}
	l.ExpiresAt = expires.UTC()
	l.UpdatedAt = now
	return l, c.store.Save(ctx, l, nil, "", "", "lease.renewed", "Lease deadline extended")
}
func (c *Controller) Lease(ctx context.Context, id string) (domain.Lease, error) {
	return c.store.Lease(ctx, id)
}
func (c *Controller) Job(ctx context.Context, id string) (domain.Job, error) {
	return c.store.Job(ctx, id)
}
func (c *Controller) Events(ctx context.Context, after int64, id string, limit int) ([]domain.Event, error) {
	return c.store.Events(ctx, after, id, limit)
}

// Recover does not repeat dispatched mutations. Clone completion cannot be
// proven by directory existence, so an interrupted clone requires inspection.
func (c *Controller) Recover(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	js, err := c.store.Jobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.State != "running" {
			continue
		}
		if j.Kind == "image_build" && strings.HasSuffix(j.Phase, "_dispatched") && j.Phase != "download_dispatched" {
			l, err := c.store.Lease(ctx, j.LeaseID)
			if err != nil {
				return err
			}
			if err := c.attention(ctx, l, j, "image_outcome_unknown", "Image mutation was interrupted; inspect the VM before cleanup. The step will not be replayed.", true); err != nil {
				return err
			}
			continue
		}
		if j.Phase == "ssh_dispatched" {
			l, err := c.store.Lease(ctx, j.LeaseID)
			if err != nil {
				return err
			}
			if err := c.attention(ctx, l, j, "ssh_outcome_unknown", "Daemon restarted during credential isolation; release the lease; isolation will not be replayed", false); err != nil {
				return err
			}
			continue
		}
		if j.Phase == "clone_dispatched" {
			l, err := c.store.Lease(ctx, j.LeaseID)
			if err != nil {
				return err
			}
			if err := c.attention(ctx, l, j, "clone_outcome_unknown", "Daemon restarted during clone. Inspect Lume; clone will not be replayed.", true); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *Controller) Run(ctx context.Context) error {
	if err := c.Recover(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	defer c.wg.Wait()
	for {
		if err := c.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			c.log.Error("reconciliation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (c *Controller) Tick(ctx context.Context) error {
	snapshotStarted := c.now()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	o, obsErr := c.backend.Observe(readCtx)
	cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	if obsErr != nil {
		if c.observation.Error == nil && ctx.Err() == nil {
			c.log.Warn("Lume observation failed; admission paused", "error", obsErr)
		}
		c.observation.Error = &domain.Error{Code: "backend_unavailable", Message: "Cannot verify Lume inventory; starts and cleanup are paused.", Retryable: true}
		return nil
	}
	if o.HostMax < 1 || o.HostUsed < 0 || o.At.IsZero() {
		return fmt.Errorf("invalid backend observation")
	}
	if c.observation.Error != nil {
		c.log.Info("Lume observation recovered", "host_used", o.HostUsed, "host_max", o.HostMax)
	}
	o.At = snapshotStarted
	c.observation = o
	ls, err := c.store.Leases(ctx)
	if err != nil {
		return err
	}
	js, err := c.store.Jobs(ctx)
	if err != nil {
		return err
	}
	for _, l := range ls {
		if l.State == "ready" && !l.UpdatedAt.After(snapshotStarted) {
			vm, exists := c.vm(l)
			if !exists || vm.State != "running" || (c.leasePreparer != nil && (l.SSH == nil || vm.IP != l.IP)) {
				l.State = "needs_attention"
				l.IP = ""
				l.Error = domain.Err("vm_drift", "Ready VM identity, address or running state changed; release the lease")
				l.UpdatedAt = c.now()
				if err := c.store.Save(ctx, l, nil, "", "", "lease.drift", l.Error.Message); err != nil {
					return err
				}
			}
		}
		hasCleanup := false
		for _, j := range js {
			if j.LeaseID == l.ID && j.Kind == "cleanup" {
				hasCleanup = true
			}
		}
		if l.Purpose != "image" && !l.ExpiresAt.After(c.now()) && l.State != "releasing" && l.State != "quarantined" && !hasCleanup && !c.active[l.ID] {
			if _, err := c.queueCleanup(ctx, l, "", ""); err != nil {
				return err
			}
		}
	}
	// Reload because expiry may have changed jobs and leases.
	js, err = c.store.Jobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.State == "needs_attention" || c.active[j.LeaseID] || j.UpdatedAt.After(snapshotStarted) {
			continue
		}
		l, err := c.store.Lease(ctx, j.LeaseID)
		if err != nil {
			return err
		}
		if err := c.advance(ctx, l, j); err != nil {
			return err
		}
	}
	return nil
}
func (c *Controller) vm(l domain.Lease) (domain.VM, bool) {
	for _, v := range c.observation.VMs {
		if v.Key() == l.Key() {
			return v, true
		}
	}
	return domain.VM{}, false
}
func (c *Controller) save(ctx context.Context, l domain.Lease, j domain.Job, typ, msg string) error {
	l.UpdatedAt = c.now()
	j.UpdatedAt = c.now()
	return c.store.Save(ctx, l, &j, "", "", typ, msg)
}
func (c *Controller) attention(ctx context.Context, l domain.Lease, j domain.Job, code, msg string, quarantine bool) error {
	j.State = "needs_attention"
	j.Error = domain.Err(code, msg)
	l.Error = j.Error
	if quarantine {
		l.State = "quarantined"
	} else {
		l.State = "needs_attention"
	}
	return c.save(ctx, l, j, "job.needs_attention", msg)
}
func (c *Controller) advance(ctx context.Context, l domain.Lease, j domain.Job) error {
	v, exists := c.vm(l)
	if !j.Deadline.After(c.now()) {
		return c.attention(ctx, l, j, "operation_timeout", "Operation deadline exceeded. Slot remains reserved until cleanup is confirmed.", j.Phase == "clone_dispatched")
	}
	if j.Kind == "image_build" {
		return c.advanceImageBuild(ctx, l, j)
	}
	if j.Kind == "image_delete" {
		return c.advanceImageDelete(ctx, l, j)
	}
	if j.Kind == "cleanup" {
		return c.cleanup(ctx, l, j, v, exists)
	}
	switch j.Phase {
	case "queued":
		if exists {
			return c.attention(ctx, l, j, "name_collision", "Generated VM name already exists; refusing to adopt or delete it.", true)
		}
		t, ok := c.templates[l.Template]
		if l.Source != nil {
			t = *l.Source
			ok = true
		}
		if !ok {
			return c.attention(ctx, l, j, "template_unavailable", "Template removed from configuration", false)
		}
		// Recheck the source immediately before cloning.
		sourceOK := false
		for _, sv := range c.observation.VMs {
			if sv.Key() == t.Location+"/"+t.Name && sv.State == "stopped" && sv.OS == "macOS" {
				sourceOK = true
			}
		}
		if !sourceOK {
			return c.attention(ctx, l, j, "template_unavailable", "Golden source is no longer stopped or present", false)
		}
		return c.dispatch(ctx, l, j, "clone_dispatched", func(ctx context.Context) error { return c.backend.Clone(ctx, t, l) })
	case "cloned":
		if !exists {
			return c.attention(ctx, l, j, "vm_missing", "Cloned VM is missing from Lume", false)
		}
		if v.State != "stopped" {
			return c.attention(ctx, l, j, "unexpected_state", "Cloned VM changed outside Virfield; start blocked", false)
		}
		// Include all other reservations and externally running machines.
		ls, err := c.store.Leases(ctx)
		if err != nil {
			return err
		}
		cap := c.capacity(ls)
		if cap.Used > cap.Limit || c.observation.HostUsed >= cap.Limit {
			return nil
		}
		return c.dispatch(ctx, l, j, "start_dispatched", func(ctx context.Context) error { return c.backend.Start(ctx, l) })
	case "start_dispatched", "waiting_ready", "ssh_configured":
		if exists && v.State == "running" && net.ParseIP(v.IP) != nil && v.SSHAvailable {
			if c.leasePreparer != nil && j.Phase != "ssh_configured" {
				l.StartPending = false
				l.IP = v.IP
				return c.prepareSSH(ctx, l, j)
			}
			if j.Phase == "ssh_configured" && (l.IP != v.IP || l.SSH == nil) {
				return c.attention(ctx, l, j, "guest_identity_failed", "Guest address changed after SSH verification; release this lease", false)
			}
			l.State = "ready"
			l.StartPending = false
			l.IP = v.IP
			l.Error = nil
			j.Phase = "ready"
			j.State = "succeeded"
			j.Error = nil
			return c.save(ctx, l, j, "lease.ready", func() string {
				if l.SSH != nil {
					return "VM lease SSH identity is isolated and authenticated"
				}
				return "VM is running; Lume reports an IP address and SSH availability"
			}())
		}
	}
	return nil
}
func (c *Controller) cleanup(ctx context.Context, l domain.Lease, j domain.Job, v domain.VM, exists bool) error {
	// A start is asynchronous. Do not treat an early 'stopped' reading as proof
	// that it cannot still boot. Require a running observation or operator resolution.
	if l.StartPending {
		if !exists || v.State != "running" {
			return nil
		}
		l.StartPending = false
	}
	if !exists {
		l.State = "released"
		l.IP = ""
		l.Error = nil
		j.State = "succeeded"
		j.Phase = "done"
		return c.save(ctx, l, j, "lease.released", "VM absent from Lume; slot released")
	}
	if !l.CloneConfirmed {
		return c.attention(ctx, l, j, "ownership_unconfirmed", "VM exists but no successful clone is recorded; refusing to delete it", true)
	}
	for _, t := range c.templates {
		if t.Location+"/"+t.Name == l.Key() {
			return c.attention(ctx, l, j, "protected_template", "VM is configured as a golden template; deletion is blocked", true)
		}
	}
	switch j.Phase {
	case "queued":
		if v.State == "stopped" {
			return c.dispatch(ctx, l, j, "delete_dispatched", func(ctx context.Context) error { return c.backend.Delete(ctx, l) })
		}
		return c.dispatch(ctx, l, j, "stop_dispatched", func(ctx context.Context) error { return c.backend.Stop(ctx, l) })
	case "stop_dispatched", "stopped":
		if v.State == "stopped" {
			return c.dispatch(ctx, l, j, "delete_dispatched", func(ctx context.Context) error { return c.backend.Delete(ctx, l) })
		}
	case "delete_dispatched":
		// Wait for absence; never blindly repeat DELETE after a lost response.
	}
	return nil
}
func (c *Controller) dispatch(ctx context.Context, l domain.Lease, j domain.Job, phase string, fn func(context.Context) error) error {
	j.Phase = phase
	j.State = "running"
	if phase == "start_dispatched" {
		l.StartPending = true
	}
	if j.Kind == "prepare" {
		l.State = "provisioning"
	}
	if err := c.save(ctx, l, j, "job.dispatched", phase); err != nil {
		return err
	}
	c.active[l.ID] = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		opCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := fn(opCtx)
		cancel()
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.active, l.ID)
		// Record using an independent bounded context even during graceful shutdown.
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer saveCancel()
		// Preserve renewals made while the external operation was in flight.
		fresh, readErr := c.store.Lease(saveCtx, l.ID)
		if readErr != nil {
			c.log.Error("read mutation result", "error", readErr)
			return
		}
		l = fresh
		if err != nil {
			if phase == "clone_dispatched" {
				err = c.attention(saveCtx, l, j, "clone_outcome_unknown", "Lume clone did not return confirmed success. Automatic replay and deletion are blocked.", true)
			} else {
				j.Error = domain.Err("outcome_unknown", "Lume mutation did not return confirmed success; observing without replay")
				err = c.save(saveCtx, l, j, "job.outcome_unknown", j.Error.Message)
			}
		} else {
			switch phase {
			case "clone_dispatched":
				l.CloneConfirmed = true
				j.Phase = "cloned"
			case "start_dispatched":
				j.Phase = "waiting_ready"
			case "stop_dispatched":
				j.Phase = "stopped"
			}
			err = c.save(saveCtx, l, j, "job.observing", "Lume accepted operation; verifying resulting state")
		}
		if err != nil {
			c.log.Error("persist mutation result", "job_id", j.ID, "error", err)
		}
	}()
	return nil
}

// Resolve is an explicit operator acknowledgement after inspecting Lume. It is
// intentionally absent from MCP: agents must not guess that an operation ended.
// It never restarts a clone or start request. Only cleanup or forgetting an
// absent/unowned VM is possible.
func (c *Controller) Resolve(ctx context.Context, id, key, vmName string, noOperationInFlight bool) (domain.Operation, error) {
	if err := validKey(key); err != nil {
		return domain.Operation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fp := fingerprint([]any{"resolve", id, vmName, noOperationInFlight})
	if op, err := c.store.Replay(ctx, key, fp); err != nil {
		return domain.Operation{}, err
	} else if op != nil {
		return *op, nil
	}
	if !noOperationInFlight {
		return domain.Operation{}, domain.Err("invalid_request", "operator must confirm no Lume operation remains in flight")
	}
	if err := c.healthy(); err != nil {
		return domain.Operation{}, err
	}
	l, err := c.store.Lease(ctx, id)
	if err != nil {
		return domain.Operation{}, err
	}
	if l.Purpose == "image" {
		return domain.Operation{}, domain.Err("invalid_request", "use image operations for permanent images")
	}
	if l.VMName != vmName {
		return domain.Operation{}, domain.Err("invalid_request", "confirmation must match the exact VM name")
	}
	if c.active[id] {
		return domain.Operation{}, domain.Err("operation_in_progress", "Lume operation is still in flight")
	}
	if l.State != "quarantined" && l.State != "needs_attention" {
		return domain.Operation{}, domain.Err("invalid_request", "only leases needing attention can be resolved")
	}
	if c.observation.At.Before(l.UpdatedAt) {
		return domain.Operation{}, &domain.Error{Code: "backend_unavailable", Message: "Wait for a fresh Lume observation after the last operation before resolving", Retryable: true}
	}
	_, exists := c.vm(l)
	// Collisions are not ours. They may be forgotten, but never adopted/deleted.
	if exists && l.Error != nil && (l.Error.Code == "name_collision" || l.Error.Code == "ownership_unconfirmed" || l.Error.Code == "protected_template") {
		return domain.Operation{}, domain.Err("ownership_unconfirmed", "refusing to delete an unowned or protected VM; remove the conflicting identity from Lume before resolving")
	}
	l.StartPending = false
	if exists {
		l.CloneConfirmed = true
	}
	return c.queueCleanup(ctx, l, key, fp)
}
