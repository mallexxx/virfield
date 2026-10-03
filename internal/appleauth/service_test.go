package appleauth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type authJobs struct{ resumed int }

func (j *authJobs) AppleAuthJob(_ context.Context, id string) (domain.XcodeRelease, error) {
	if id != "job-one" || j.resumed != 0 {
		return domain.XcodeRelease{}, domain.Err("invalid_request", "not waiting")
	}
	return domain.XcodeRelease{Version: "13.4.1", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip"}, nil
}
func (j *authJobs) ResumeAppleDownload(_ context.Context, id string) error {
	if id != "job-one" {
		return domain.Err("invalid_request", "wrong job")
	}
	j.resumed++
	return nil
}

func TestLocalSignInLinkResumesOnlyItsJobAndStoresPrivateCookies(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"op":"login"'*) printf '%s\n' '{"state":"code","message":"Enter code"}' ;;
    *'"op":"code"'*) printf '%s\n' '{"state":"authenticated","cookies":".apple.com\tTRUE\t/\tTRUE\t0\tADCDownloadAuth\tfixture\n"}' ;;
    *) printf '%s\n' '{"state":"error","message":"bad step"}' ;;
  esac
done
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	jobs := &authJobs{}
	cookiePath := filepath.Join(dir, "private", "cookies.txt")
	s, err := New(helper, cookiePath, jobs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(context.Background(), "other-job"); err == nil {
		t.Fatal("unrelated job received sign-in link")
	}
	old, err := s.Begin(context.Background(), "job-one")
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Begin(context.Background(), "job-one")
	if err != nil || token == old {
		t.Fatal("link was not rotated", err)
	}
	if _, err := s.Session(old); err == nil {
		t.Fatal("old link still works")
	}
	response, err := s.Exchange(context.Background(), token, Request{Op: "login", Account: "account@example.com", Password: "private-password"})
	if err != nil || response.State != "code" || jobs.resumed != 0 {
		t.Fatal(response, err)
	}
	response, err = s.Exchange(context.Background(), token, Request{Op: "code", Code: "123456"})
	if err != nil || response.State != "authenticated" || response.Cookies != "" || jobs.resumed != 1 {
		t.Fatal(response, err, jobs.resumed)
	}
	if _, err := s.Session(token); err == nil {
		t.Fatal("used link still works")
	}
	contents, err := os.ReadFile(cookiePath)
	if err != nil || !strings.Contains(string(contents), "ADCDownloadAuth") || strings.Contains(string(contents), "private-password") {
		t.Fatal("private cookie file was not written correctly", err)
	}
	stat, err := os.Stat(cookiePath)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("cookie file is not private", err)
	}
}

func TestAppleLinkExpires(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(helper, filepath.Join(dir, "cookies"), &authJobs{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	token, err := s.Begin(context.Background(), "job-one")
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, err := s.Session(token); err == nil {
		t.Fatal("expired link accepted")
	}
}

func TestAppleBrowserSessionResumesExactJobWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	jobs := &authJobs{}
	cookiePath := filepath.Join(dir, "private", "cookies.txt")
	s, err := New(helper, cookiePath, jobs)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Begin(context.Background(), "job-one")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Session(token)
	if err != nil || page.AppleURL != "https://developer.apple.com/services-account/download?path=%2FDeveloper_Tools%2FXcode_13.4.1%2FXcode_13.4.1.xip" {
		t.Fatal(page.AppleURL, err)
	}
	if err := s.ResumeBrowserCookies(context.Background(), token, "evil.com\tTRUE\t/\tTRUE\t0\tsession\tfixture\n"); err == nil || jobs.resumed != 0 {
		t.Fatal("non-Apple session accepted")
	}
	cookies := ".apple.com\tTRUE\t/\tTRUE\t0\tADCDownloadAuth\tfixture\n"
	if err := s.ResumeBrowserCookies(context.Background(), token, cookies); err != nil || jobs.resumed != 1 {
		t.Fatal(err, jobs.resumed)
	}
	if _, err := s.Session(token); err == nil {
		t.Fatal("used browser link still works")
	}
	contents, err := os.ReadFile(cookiePath)
	if err != nil || string(contents) != cookies {
		t.Fatal("download session not stored privately", err)
	}
	stat, err := os.Stat(cookiePath)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("download session file permissions", err)
	}
}

func TestCookiePathMustCoverDeveloperTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies")
	for _, scope := range []string{"/", "/Developer_Tools/", "/Developer_Tools/Xcode/"} {
		cookie := ".apple.com\tTRUE\t" + scope + "\tTRUE\t0\tADCDownloadAuth\tfixture\n"
		if err := writeCookies(path, cookie); err != nil {
			t.Fatalf("valid scope %q rejected: %v", scope, err)
		}
	}
	for _, scope := range []string{"", "/D", "/other/"} {
		cookie := ".apple.com\tTRUE\t" + scope + "\tTRUE\t0\tADCDownloadAuth\tfixture\n"
		if err := writeCookies(path, cookie); err == nil {
			t.Fatalf("invalid scope %q accepted", scope)
		}
	}
}
