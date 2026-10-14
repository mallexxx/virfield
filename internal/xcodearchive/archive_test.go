package xcodearchive

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"github.com/mallexxx/virfield/internal/domain"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
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
	e := &Store{CacheDir: cache, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Range") != "bytes=5-" {
			t.Error(r.Header)
		}
		return &http.Response{StatusCode: 206, ContentLength: int64(len(data) - 5), Header: http.Header{"Content-Range": {fmt.Sprintf("bytes 5-%d/%d", len(data)-1, len(data))}}, Body: io.NopCloser(strings.NewReader(data[5:]))}, nil
	})}}
	for range 2 {
		path, err := e.Archive(context.Background(), x, func(string) error { return nil })
		if err != nil || path != filepath.Join(cache, x.SHA1+".xip") {
			t.Fatal(path, err)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestXcodeRetriesInterruptedTransfer(t *testing.T) {
	data := "retryable signed archive"
	x := xcodeFixture(data)
	calls := 0
	e := &Store{CacheDir: t.TempDir(), HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary connection reset")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}}
	if _, err := e.Archive(context.Background(), x, func(string) error { return nil }); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
}
func TestXcodeCorruptFinalIsQuarantinedAndReplaced(t *testing.T) {
	data := "correct archive"
	x := xcodeFixture(data)
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache", "xcode")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(cache, x.SHA1+".xip")
	if err := os.WriteFile(final, []byte("wrong archive"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e := &Store{CacheDir: cache, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Range") != "" {
			t.Fatal("corrupt final sent a range request")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}}
	if e.CachedXcode(x) {
		t.Fatal("unverified archive reported downloaded")
	}
	if _, err := e.Archive(context.Background(), x, func(string) error { return nil }); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	if !e.CachedXcode(x) {
		t.Fatal("verified archive not reported downloaded")
	}
	quarantined, err := filepath.Glob(final + ".corrupt-*")
	if err != nil || len(quarantined) != 1 {
		t.Fatal(quarantined, err)
	}
}
func TestXcodeFullCorruptPartialRecoversAfterRange416(t *testing.T) {
	data := "correct archive"
	x := xcodeFixture(data)
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache", "xcode")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(cache, x.SHA1+".xip.partial")
	if err := os.WriteFile(partial, []byte("wrong archive!!"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e := &Store{CacheDir: cache, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 416, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		if r.Header.Get("Range") != "" {
			t.Fatal("retry sent a range request")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}}
	if _, err := e.Archive(context.Background(), x, func(string) error { return nil }); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
}
func TestXcodeCorruptResumedBytesRetryFromZero(t *testing.T) {
	data := "correct archive"
	x := xcodeFixture(data)
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache", "xcode")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, x.SHA1+".xip.partial"), []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e := &Store{CacheDir: cache, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if r.Header.Get("Range") != "bytes=5-" {
				t.Fatal(r.Header)
			}
			return &http.Response{StatusCode: 206, ContentLength: int64(len(data) - 5), Header: http.Header{"Content-Range": {fmt.Sprintf("bytes 5-%d/%d", len(data)-1, len(data))}}, Body: io.NopCloser(strings.NewReader(data[5:]))}, nil
		}
		if r.Header.Get("Range") != "" {
			t.Fatal("retry used bad range")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}}
	if _, err := e.Archive(context.Background(), x, func(string) error { return nil }); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
}
func TestXcodeFollowsAppleCDNRedirectWithoutForwardingCookies(t *testing.T) {
	data := "signed archive fixture"
	x := xcodeFixture(data)
	dir := t.TempDir()
	cookies := filepath.Join(dir, "cookies")
	if err := os.WriteFile(cookies, []byte(".apple.com\tTRUE\t/\tTRUE\t0\tADCDownloadAuth\tfixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e := &Store{CacheDir: filepath.Join(dir, "cache", "xcode"), CookiePath: cookies, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if r.Header.Get("Cookie") != "ADCDownloadAuth=fixture" {
				t.Error("initial Apple request missed session cookie")
			}
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://devimages-cdn.apple.com/Xcode.xip"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		if r.URL.Host != "devimages-cdn.apple.com" || r.Header.Get("Cookie") != "" {
			t.Error("cookie leaked across redirect", r.URL, r.Header.Get("Cookie"))
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}}
	if _, err := e.Archive(context.Background(), x, func(string) error { return nil }); err != nil || calls != 2 {
		t.Fatal(err, calls)
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
