package images

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/lume"
)

func TestImageStopNeverForcesPowerOffWithoutGuestIdentity(t *testing.T) {
	for _, state := range []string{"running", "stopped", "unknown"} {
		t.Run(state, func(t *testing.T) {
			var mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					mutations.Add(1)
					fmt.Fprint(w, `{}`)
					return
				}
				switch r.URL.Path {
				case "/lume/host/status":
					fmt.Fprint(w, `{"vm_count":1,"max_vms":2,"available_slots":1,"status":"healthy"}`)
				case "/lume/vms":
					fmt.Fprintf(w, `[{"name":"golden","locationName":"home","os":"macOS","status":%q,"ipAddress":"127.0.0.1","sshAvailable":true}]`, state)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			backend, err := lume.New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			engine := &Engine{Dir: t.TempDir(), Backend: backend}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = engine.stop(ctx, domain.Lease{ID: "image-test", VMName: "golden", Location: "home", Purpose: "image"})
			if (err == nil) != (state == "stopped") {
				t.Fatalf("state %s: %v", state, err)
			}
			if mutations.Load() != 0 {
				t.Fatal("unsafe hypervisor stop fallback", mutations.Load())
			}
		})
	}
}
