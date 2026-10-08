package nodeimage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type fakeRuntime struct {
	mu      sync.Mutex
	calls   []string
	running bool
	port    int
}

func (r *fakeRuntime) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *fakeRuntime) Observe(context.Context) (domain.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ip := "stopped", ""
	if r.running {
		state, ip = "running", "192.168.1.86"
	}
	return domain.Observation{VMs: []domain.VM{{Name: "htpc-sonoma", Location: "htpc", State: state, IP: ip, SSHPort: r.port, SSHAvailable: r.running}}}, nil
}

func (r *fakeRuntime) CloneImage(_ context.Context, _ domain.Lease, golden string) error {
	r.record("clone:" + golden)
	return nil
}

func (r *fakeRuntime) StartImage(context.Context, domain.Lease) error {
	r.record("start")
	r.mu.Lock()
	r.running = true
	r.mu.Unlock()
	return nil
}

func (r *fakeRuntime) StopImage(context.Context, domain.Lease) error {
	r.record("stop")
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
	return nil
}

func (r *fakeRuntime) CommitImage(context.Context, domain.Lease) error {
	r.record("commit")
	return nil
}

type fakeSession struct {
	rt  *fakeRuntime
	sip string
}

func (s *fakeSession) Run(_ context.Context, command, _ string) (string, error) {
	switch {
	case strings.Contains(command, "-buildVersion"):
		return "23J631\n", nil
	case strings.Contains(command, "-productVersion"):
		return "14.8.9\n", nil
	case strings.Contains(command, "csrutil"):
		return s.sip + "\n", nil
	case strings.Contains(command, "shutdown"):
		s.rt.record("shutdown")
	}
	return "", nil
}

func (s *fakeSession) RunReader(ctx context.Context, _ time.Duration, command string, _ io.Reader) (string, error) {
	return s.Run(ctx, command, "")
}

func (s *fakeSession) Close() {}

type fakeGuests struct {
	rt    *fakeRuntime
	sip   string
	ports []int
}

func (g *fakeGuests) Connect(_ context.Context, l domain.Lease, ip string, bootstrap bool) (Session, error) {
	g.ports = append(g.ports, l.SSHPort)
	if ip != "192.168.1.86" {
		return nil, errors.New("wrong guest address " + ip)
	}
	if bootstrap {
		g.rt.record("connect:bootstrap")
	} else {
		g.rt.record("connect:secured")
	}
	return &fakeSession{rt: g.rt, sip: g.sip}, nil
}

func (g *fakeGuests) Secure(context.Context, domain.Lease, string, Session) error {
	g.rt.record("secure")
	return nil
}

func (g *fakeGuests) Password(domain.Lease) (string, error) { return "rotated", nil }

func testBuilder(t *testing.T, sip string) (*Builder, *fakeRuntime, *fakeGuests) {
	t.Helper()
	rt := &fakeRuntime{port: 22102}
	g := &fakeGuests{rt: rt, sip: sip}
	b := New(Config{StateDir: t.TempDir(), Runtime: rt, Guests: g})
	b.poll, b.shutdownGrace = time.Millisecond, 0
	b.desktop = func(context.Context, Session) error { return nil }
	return b, rt, g
}

var (
	imageLease   = domain.Lease{ID: "image-0123456789abcdef", VMName: "htpc-sonoma", Location: "htpc", Purpose: "image", GuestUser: "admin"}
	imageProfile = domain.ImageProfile{Node: &domain.NodeSource{Golden: "sonoma-bootstrap"}, MacOS: "14.8.9", Build: "23J631", DisableSIP: true}
)

func TestStepsSecureRebootVerifyAndCommit(t *testing.T) {
	b, rt, g := testBuilder(t, "System Integrity Protection status: disabled.")
	for _, step := range []string{"build", "qualify", "stop", "commit"} {
		if err := b.Step(context.Background(), imageLease, imageProfile, step, func(string) error { return nil }); err != nil {
			t.Fatal(step, err)
		}
	}
	want := "clone:sonoma-bootstrap,start,connect:bootstrap,secure,shutdown,stop,start,connect:secured,shutdown,stop,stop,commit"
	if strings.Join(rt.calls, ",") != want {
		t.Fatal(strings.Join(rt.calls, ","))
	}
	for _, port := range g.ports {
		if port != 22102 {
			t.Fatal("guest SSH not on the image VM port", g.ports)
		}
	}
	evidence, err := os.ReadFile(filepath.Join(b.folder(imageLease), "verification.json"))
	if err != nil || !strings.Contains(string(evidence), `"golden": "sonoma-bootstrap"`) || !strings.Contains(string(evidence), "disabled") {
		t.Fatal(string(evidence), err)
	}
}

func TestQualifyHaltsOnWrongSIP(t *testing.T) {
	b, rt, _ := testBuilder(t, "System Integrity Protection status: enabled.")
	err := b.Step(context.Background(), imageLease, imageProfile, "qualify", func(string) error { return nil })
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != "sip_verification_failed" {
		t.Fatal(err)
	}
	if rt.running || rt.calls[len(rt.calls)-1] != "stop" {
		t.Fatal("failed qualification left the image VM running", rt.calls)
	}
}

func TestStepRefusesForeignProfilesAndNames(t *testing.T) {
	b, _, _ := testBuilder(t, "")
	tart := imageProfile
	tart.Node = nil
	if b.Step(context.Background(), imageLease, tart, "build", nil) == nil {
		t.Fatal("built a non-node profile")
	}
	lease := imageLease
	lease.VMName = "vf-0123456789abcdef"
	if b.Step(context.Background(), lease, imageProfile, "build", nil) == nil {
		t.Fatal("used a lease name for an image")
	}
}
