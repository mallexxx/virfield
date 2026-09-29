package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/control"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/store"
)

type backend struct{}

func (backend) Observe(context.Context) (domain.Observation, error) {
	return domain.Observation{HostMax: 2, At: time.Now(), VMs: []domain.VM{{Name: "golden", Location: "home", OS: "macOS", State: "stopped"}}}, nil
}
func (backend) Clone(context.Context, domain.Template, domain.Lease) error {
	panic("API must not execute mutations inline")
}
func (backend) Start(context.Context, domain.Lease) error  { panic("unexpected mutation") }
func (backend) Stop(context.Context, domain.Lease) error   { panic("unexpected mutation") }
func (backend) Delete(context.Context, domain.Lease) error { panic("unexpected mutation") }

const token = "0123456789012345678901234567890123456789"

func handler(t *testing.T) http.Handler {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := control.New(s, backend{}, []domain.Template{{ID: "test", Name: "golden", Location: "home"}}, 2, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	return New(c, token, log)
}
func request(h http.Handler, method, path, body, key string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	if auth {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthEveryAPIRouteAndNoCrossOrigin(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/api/v1/status", "/api/v1/events", "/api/v1/leases/unknown", "/api/v1/jobs/unknown"} {
		if w := request(h, "GET", path, "", "", false); w.Code != 401 {
			t.Fatalf("%s %d", path, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://localhost/api/v1/status", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestStatusBeforeFirstBackendObservation(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := control.New(s, backend{}, []domain.Template{{ID: "test", Name: "golden", Location: "home"}}, 2, log)
	if err != nil {
		t.Fatal(err)
	}
	w := request(New(c, token, log), "GET", "/api/v1/status", "", "", true)
	var status domain.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || status.Observation.VMs == nil || status.Observation.Error == nil || status.Capacity.Available != 0 {
		t.Fatal("cold-start status must be renderable and refuse admission", w.Code, w.Body.String())
	}
}
func TestAcquireReplayCapacityAndStrictJSON(t *testing.T) {
	h := handler(t)
	body := `{"template":"test","ttl_seconds":3600}`
	w := request(h, "POST", "/api/v1/leases", body, "request-1", true)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Location") == "" {
		t.Fatal("missing job Location")
	}
	w = request(h, "POST", "/api/v1/leases", body, "request-1", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/leases", body, "request-2", true)
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	w = request(h, "POST", "/api/v1/leases", body, "request-3", true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "capacity_exhausted") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, bad := range []string{body + body, `{"template":"test","ttl_seconds":3600,"command":"sh"}`, `{"template":"../bad","ttl_seconds":3600}`, `null`, strings.Repeat("x", 17000)} {
		w = request(h, "POST", "/api/v1/leases", bad, "request-x", true)
		if w.Code != 400 {
			t.Fatalf("bad JSON got %d", w.Code)
		}
	}
}
func TestEventCursorAndBounds(t *testing.T) {
	h := handler(t)
	request(h, "POST", "/api/v1/leases", `{"template":"test","ttl_seconds":3600}`, "request-1", true)
	w := request(h, "GET", "/api/v1/events?after=0&limit=1", "", "", true)
	var page struct {
		Events []domain.Event `json:"events"`
		Next   int64          `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Next != 1 {
		t.Fatal(w.Body.String())
	}
	for _, query := range []string{"after=-1", "after=nan", "limit=0", "limit=501"} {
		w = request(h, "GET", "/api/v1/events?"+query, "", "", true)
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
}
func TestEmbeddedConsoleHasSecurityHeaders(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		w := request(h, "GET", path, "", "", false)
		if w.Code != 200 || w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, w.Code)
		}
	}
}

func TestImageDeleteRequiresAuthAndExactConfirmation(t *testing.T) {
	h := handler(t)
	for _, route := range []string{"build", "delete", "recover"} {
		if w := request(h, "POST", "/api/v1/images/test/"+route, `{}`, "image-api-auth", false); w.Code != 401 {
			t.Fatal(route, w.Code)
		}
	}
	w := request(h, "POST", "/api/v1/images/test/delete", `{"confirm_name":"wrong"}`, "image-api-wrong", true)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/images/test/delete", `{"confirm_name":"golden","command":"rm"}`, "image-api-extra", true)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/images/test/delete", `{"confirm_name":"golden"}`, "image-api-delete", true)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/leases", `{"template":"test","ttl_seconds":3600}`, "image-api-blocked", true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "image_in_use") {
		t.Fatal(w.Code, w.Body.String())
	}
}
