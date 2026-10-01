package images

import (
	"context"
	"crypto/sha1"
	"fmt"
	"github.com/mallexxx/virfield/internal/domain"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func xcodeFixture(data string) domain.XcodeRelease {
	return domain.XcodeRelease{Version: "13.4.1", Build: "13F100", Requires: "12.0", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1/Xcode_13.4.1.xip", SHA1: fmt.Sprintf("%x", sha1.Sum([]byte(data)))}
}
func TestXcodeDownloadResumeAndVerify(t *testing.T) {
	data := "signed archive fixture"
	x := xcodeFixture(data)
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache", "xcode")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, x.SHA1+".xip.partial"), []byte(data[:5]), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e := &Engine{Dir: dir, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Range") != "bytes=5-" {
			t.Error(r.Header)
		}
		return &http.Response{StatusCode: 206, ContentLength: int64(len(data) - 5), Header: http.Header{"Content-Range": {fmt.Sprintf("bytes 5-%d/%d", len(data)-1, len(data))}}, Body: io.NopCloser(strings.NewReader(data[5:]))}, nil
	})}}
	for range 2 {
		path, err := e.xcodeArchive(context.Background(), x, func(string) error { return nil })
		if err != nil || path != filepath.Join(cache, x.SHA1+".xip") {
			t.Fatal(path, err)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestXcodeDoesNotFollowLoginRedirect(t *testing.T) {
	calls := 0
	e := &Engine{Dir: t.TempDir(), HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://developer.apple.com/unauthorized/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	_, err := e.xcodeArchive(context.Background(), xcodeFixture("archive"), func(string) error { return nil })
	if f, ok := err.(*domain.Error); !ok || f.Code != "apple_auth_required" || calls != 1 {
		t.Fatal(err, calls)
	}
}
func TestXcodeImportRequiresMatchingChecksum(t *testing.T) {
	imports := t.TempDir()
	data := "correct archive"
	x := xcodeFixture(data)
	e := &Engine{Dir: t.TempDir(), Tools: domain.ImageTools{XcodeArchives: imports}}
	path := filepath.Join(imports, "Xcode_13.4.1.xip")
	if err := os.WriteFile(path, []byte("corrupt archive"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := e.xcodeArchive(context.Background(), x, func(string) error { return nil })
	if f, ok := err.(*domain.Error); !ok || f.Code != "download_integrity" {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.xcodeArchive(context.Background(), x, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestAppleCookiesAreScopedAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies")
	contents := ".apple.com\tTRUE\t/\tTRUE\t0\tADCDownloadAuth\tfixture\n.evil.example\tTRUE\t/\tTRUE\t0\tsecret\tdontsend\n.apple.com\tTRUE\t/\tTRUE\t1\texpired\tdontsend\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := appleCookies(path)
	if err != nil || value != "ADCDownloadAuth=fixture" {
		t.Fatal(value, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := appleCookies(path); err == nil {
		t.Fatal("public cookie file accepted")
	}
}

func TestAppleSignatureRequirementSyntax(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires native Apple requirement compiler")
	}
	cmd := exec.Command("/usr/bin/csreq", "-r", appleCodeRequirement, "-t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Apple signature requirement rejected: %s: %v", out, err)
	}
}
