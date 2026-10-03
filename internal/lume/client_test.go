package lume

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestInstalledHTTPContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/lume/host/status":
			fmt.Fprint(w, `{"status":"healthy","vm_count":0,"max_vms":2,"available_slots":2}`)
		case "/lume/vms":
			fmt.Fprint(w, `[{"name":"golden","locationName":"home","os":"macOS","status":"stopped","ipAddress":null,"sshAvailable":null}]`)
		case "/lume/vms/clone":
			var v map[string]string
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				t.Error(err)
			}
			if v["name"] != "golden" || v["newName"] != "vf-test" || v["sourceLocation"] != "home" || v["destLocation"] != "home" {
				t.Error(v)
			}
			fmt.Fprint(w, `{}`)
		case "/lume/vms/vf-test/run":
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			var v struct {
				NoDisplay              bool              `json:"noDisplay"`
				NoDisplayVapor         bool              `json:"no_display"`
				Storage                string            `json:"storage"`
				SharedDirectories      []SharedDirectory `json:"sharedDirectories"`
				SharedDirectoriesVapor []SharedDirectory `json:"shared_directories"`
			}
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				t.Error(err)
			}
			if !v.NoDisplay || !v.NoDisplayVapor || v.Storage != "home" || v.SharedDirectories != nil || v.SharedDirectoriesVapor != nil {
				t.Error(v)
			}
			w.WriteHeader(202)
			fmt.Fprint(w, `{"message":"VM start initiated"}`)
		case "/lume/vms/vf-test/stop":
			fmt.Fprint(w, `{}`)
		case "/lume/vms/vf-test":
			if r.Method != "DELETE" || r.URL.Query().Get("storage") != "home" {
				t.Error(r.Method, r.URL)
			}
			w.WriteHeader(200) // Lume delete may be empty.
		default:
			t.Error(r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	o, err := c.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.VMs) != 1 || o.VMs[0].SSHAvailable {
		t.Fatal(o)
	}
	l := domain.Lease{VMName: "vf-test", Location: "home"}
	tm := domain.Template{Name: "golden", Location: "home"}
	for _, fn := range []func() error{func() error { return c.Clone(ctx, tm, l) }, func() error { return c.Start(ctx, l) }, func() error { return c.Stop(ctx, l) }, func() error { return c.Delete(ctx, l) }} {
		if err := fn(); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 6 {
		t.Fatal(requests)
	}
}

func TestStartWithSharedDirectories(t *testing.T) {
	var body struct {
		NoDisplay              bool     `json:"noDisplay"`
		NoDisplayVapor         bool     `json:"no_display"`
		Storage                string   `json:"storage"`
		SharedDirectories      []string `json:"sharedDirectories"`
		SharedDirectoriesVapor []string `json:"shared_directories"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lume/vms/vf-test/run" {
			t.Error(r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	c, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	err = c.StartWithOptions(context.Background(), domain.Lease{VMName: "vf-test", Location: "home"}, StartOptions{SharedDirectories: []SharedDirectory{"/tmp/virfield-share"}})
	if err != nil {
		t.Fatal(err)
	}
	if !body.NoDisplay || !body.NoDisplayVapor || body.Storage != "home" || len(body.SharedDirectories) != 1 || body.SharedDirectories[0] != "/tmp/virfield-share" {
		t.Fatal(body)
	}
	if len(body.SharedDirectoriesVapor) != 1 || body.SharedDirectoriesVapor[0] != "/tmp/virfield-share" {
		t.Fatal(body)
	}
	if err := c.StartWithOptions(context.Background(), domain.Lease{VMName: "vf-test", Location: "home"}, StartOptions{SharedDirectories: []SharedDirectory{"relative"}}); err == nil {
		t.Fatal("relative shared path accepted")
	}
}

func TestNoMutationRetryAndNoUpstreamSecretLeak(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "secret-token", 500) }))
	defer s.Close()
	c, _ := New(s.URL)
	err := c.Start(context.Background(), domain.Lease{VMName: "test"})
	if err == nil || calls != 1 {
		t.Fatalf("%v %d", err, calls)
	}
	if err.Error() != "lume POST returned HTTP 500" {
		t.Fatal(err)
	}
}
func TestMalformedInventoryFailClosed(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `[{"name":"x"}]`, `not json`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/lume/host/status" {
					fmt.Fprint(w, `{"status":"healthy","vm_count":0,"max_vms":2,"available_slots":2}`)
				} else {
					fmt.Fprint(w, body)
				}
			}))
			defer s.Close()
			c, _ := New(s.URL)
			if _, err := c.Observe(context.Background()); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}
func TestRemoteLumeRejected(t *testing.T) {
	for _, u := range []string{"http://example.com:7777", "file:///etc/passwd", "http://localhost:7777/lume", "http://user:pass@localhost:7777"} {
		if _, err := New(u); err == nil {
			t.Fatal(u)
		}
	}
}
