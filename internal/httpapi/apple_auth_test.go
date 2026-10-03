package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/appleauth"
	"github.com/mallexxx/virfield/internal/control"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/store"
)

type browserJobs struct{ resumed bool }

func (j *browserJobs) AppleAuthJob(_ context.Context, id string) (domain.XcodeRelease, error) {
	if id != "job-one" || j.resumed {
		return domain.XcodeRelease{}, domain.Err("invalid_request", "not waiting")
	}
	return domain.XcodeRelease{Version: "13.4.1", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip"}, nil
}
func (j *browserJobs) ResumeAppleDownload(_ context.Context, id string) error {
	if id != "job-one" {
		return domain.Err("invalid_request", "wrong job")
	}
	j.resumed = true
	return nil
}

func TestAppleLinkOpensAppleBrowserAndResumesOnlyAfterPrivateSession(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' '{"state":"authenticated","cookies":".apple.com\tTRUE\t/\tTRUE\t0\tADCDownloadAuth\tfixture\n"}'
done
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	jobs := &browserJobs{}
	auth, err := appleauth.New(helper, filepath.Join(dir, "cookies"), jobs)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager, err := control.New(db, backend{}, nil, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := New(manager, token, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{AppleAuth: auth, Origin: "http://127.0.0.1:7780"})
	path := "/api/v1/jobs/job-one/apple-auth"
	if got := request(h, "POST", path, `{}`, "", false); got.Code != 401 {
		t.Fatal(got.Code)
	}
	link := request(h, "POST", path, `{}`, "", true)
	if link.Code != 200 {
		t.Fatal(link.Code, link.Body.String())
	}
	var issued struct {
		URL       string    `json:"url"`
		DeepLink  string    `json:"deep_link"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(link.Body.Bytes(), &issued); err != nil || !strings.HasPrefix(issued.URL, "http://127.0.0.1:7780/apple-auth/") {
		t.Fatal(issued, err)
	}
	if !strings.HasPrefix(issued.DeepLink, "virfield-apple-auth://start?") || !strings.Contains(issued.DeepLink, "Xcode_13.4.1.xip") {
		t.Fatal("missing direct Apple browser link", issued.DeepLink)
	}
	get := httptest.NewRequest("GET", issued.URL, nil)
	page := httptest.NewRecorder()
	h.ServeHTTP(page, get)
	if page.Code != 302 || page.Header().Get("Location") != issued.DeepLink || page.Body.Len() != 0 {
		t.Fatal("one-click link must redirect directly to the app", page.Code, page.Header(), page.Body.String())
	}
	if got := page.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("browser redirect must not leak its secret link, got %q", got)
	}
	manual := httptest.NewRecorder()
	h.ServeHTTP(manual, httptest.NewRequest("GET", issued.URL+"?manual=1", nil))
	if manual.Code != 200 || !strings.Contains(manual.Body.String(), "Open Apple Developer sign-in") ||
		strings.Contains(manual.Body.String(), "name=\"password\"") {
		t.Fatal("fallback page unavailable", manual.Code, manual.Body.String())
	}
	body := `{"cookies":".apple.com\tTRUE\t/\tTRUE\t0\tADCDownloadAuth\tfixture\n"}`
	post := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", issued.URL+"/session", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := post("https://evil.example"); w.Code != 403 || jobs.resumed {
		t.Fatal("cross-origin session accepted", w.Code)
	}
	w := post("")
	if w.Code != 202 || !jobs.resumed || strings.Contains(w.Body.String(), "ADCDownloadAuth") {
		t.Fatal(w.Code, w.Body.String())
	}
}
