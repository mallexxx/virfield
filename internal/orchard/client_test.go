package orchard

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestObserveCapacityAndCreateContract(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	created := vm{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "virfield" || password != "secret" {
			t.Error("missing Orchard service account authentication")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/workers":
			fmt.Fprintf(w, `[{"name":"mac-one","last_seen":%q,"resources":{"org.cirruslabs.logical-cores":8,"org.cirruslabs.memory-mib":12288,"org.cirruslabs.tart-vms":2,"ai.virfield.physical-memory-mib":16384},"labels":{"pool":"personal"},"arch":"arm64","runtime":"tart"}]`, now.Format(time.RFC3339))
		case "GET /v1/vms":
			fmt.Fprint(w, `[{"name":"vf-one","status":"running","powerState":"running","worker":"mac-one","assignedCPU":4,"assignedMemory":4096,"diskSize":80,"os":"darwin"}]`)
		case "GET /v1/vms/vf-one/ip":
			fmt.Fprint(w, `{"ip":"192.168.64.10"}`)
		case "POST /v1/vms":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := New(Config{
		URL: server.URL, ServiceAccount: "virfield", Token: "secret", PoolID: "personal", MaxVMs: 4,
		WorkerLabels:              map[string]string{"pool": "personal"},
		HostLimits:                map[string]HostLimit{"mac-one": {MaxVMs: 2, MemoryReserveBytes: 4 << 30}},
		WorkerOfflineAfterSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return now }
	observation, err := client.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Hosts) != 1 || observation.Hosts[0].Availability != "available" {
		t.Fatal(observation.Hosts)
	}
	capacity := observation.Hosts[0].Capacity
	if capacity.MemoryReserveBytes != 4<<30 || capacity.Allocated.MemoryBytes != 4<<30 || capacity.Available.MemoryBytes != 8<<30 || capacity.UsedVMs != 1 {
		t.Fatalf("capacity = %#v", capacity)
	}
	if observation.VMs[0].IP != "192.168.64.10" || observation.StorageContainers[0].PoolID != "personal" {
		t.Fatal(observation)
	}
	template := domain.Template{Source: "ghcr.io/cirrus/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OS: "macOS", Arch: "arm64", Resources: domain.Resources{CPU: 4, MemoryBytes: 8 << 30, DiskBytes: 100_000_000_000}}
	if err := client.Clone(context.Background(), template, domain.Lease{VMName: "vf-new"}); err != nil {
		t.Fatal(err)
	}
	if created.Image != template.Source || !created.Headless || created.Resources["org.cirruslabs.memory-mib"] != 8192 || created.Resources["org.cirruslabs.tart-vms"] != 1 || created.Labels["pool"] != "personal" {
		t.Fatalf("created = %#v", created)
	}
}

func TestCapacityPolicyMismatchClosesAdmission(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/workers":
			fmt.Fprintf(w, `[{"name":"mac-one","last_seen":%q,"resources":{"org.cirruslabs.memory-mib":16384,"org.cirruslabs.tart-vms":2,"ai.virfield.physical-memory-mib":16384},"arch":"arm64","runtime":"tart"}]`, now.Format(time.RFC3339))
		case "/v1/vms":
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	client, err := New(Config{URL: server.URL, ServiceAccount: "virfield", Token: "secret", PoolID: "personal", MaxVMs: 4, HostLimits: map[string]HostLimit{"mac-one": {MaxVMs: 2, MemoryReserveBytes: 4 << 30}}})
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return now }
	observation, err := client.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if observation.HostUsed != observation.HostMax || observation.Hosts[0].UnavailableReason != "capacity_policy_mismatch" {
		t.Fatalf("unsafe worker did not close admission: %#v", observation)
	}
}

func TestWakeHostSendsMagicPacketToConfiguredRelay(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client := &Client{config: Config{HostLimits: map[string]HostLimit{
		"mac-one": {WakeOnLAN: &WakeOnLAN{MACAddress: "00:11:22:33:44:55", BroadcastAddress: listener.LocalAddr().String()}},
	}}}
	if err := client.WakeHost(context.Background(), "mac-one"); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 256)
	n, _, err := listener.ReadFromUDP(packet)
	if err != nil {
		t.Fatal(err)
	}
	if n != 102 || string(packet[:6]) != string([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) {
		t.Fatalf("invalid magic packet: %d bytes", n)
	}
}

func TestConfiguredHostRemainsVisibleWhenWorkerIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[]`) }))
	defer server.Close()
	client, err := New(Config{
		URL: server.URL, ServiceAccount: "virfield", Token: "secret", PoolID: "personal", MaxVMs: 2,
		HostLimits: map[string]HostLimit{"old-macbook": {MaxVMs: 2, MemoryReserveBytes: 4 << 30}},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Hosts) != 1 || observation.Hosts[0].ID != "old-macbook" || observation.Hosts[0].UnavailableReason != "host_unobserved" || len(observation.StorageContainers) != 1 {
		t.Fatalf("configured host disappeared: %#v", observation)
	}
}
