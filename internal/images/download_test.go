package images

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func profile(data string) domain.ImageProfile {
	return domain.ImageProfile{URL: "https://updates.cdn-apple.com/test.ipsw", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(data))), Size: int64(len(data)), Build: "26A428"}
}
func TestDownloadResumeAndDigest(t *testing.T) {
	data := "a pinned restore image"
	p := profile(data)
	dir := t.TempDir()
	path := filepath.Join(dir, p.SHA256+".ipsw")
	if err := os.WriteFile(path+".partial", []byte(data[:5]), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	c := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Range") != "bytes=5-" {
			t.Error(r.Header)
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": {fmt.Sprintf("bytes 5-%d/%d", len(data)-1, len(data))}}, Body: io.NopCloser(strings.NewReader(data[5:])), ContentLength: int64(len(data) - 5)}, nil
	})}
	got, err := Download(context.Background(), c, dir, p, func(string) error { return nil })
	if err != nil || got != path {
		t.Fatal(got, err)
	}
	if _, err := Download(context.Background(), c, dir, p, func(string) error { return nil }); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	if _, err := os.Stat(path + ".partial"); !os.IsNotExist(err) {
		t.Fatal("partial not atomically promoted")
	}
}
func TestDownloadCorruptResumedBytesRetryFromZero(t *testing.T) {
	data := "correct restore image"
	p := profile(data)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, p.SHA256+".ipsw.partial"), []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	c := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 206, ContentLength: int64(len(data) - 5), Header: http.Header{"Content-Range": {fmt.Sprintf("bytes 5-%d/%d", len(data)-1, len(data))}}, Body: io.NopCloser(strings.NewReader(data[5:]))}, nil
		}
		if r.Header.Get("Range") != "" {
			t.Fatal("retry used bad range")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}
	if _, err := Download(context.Background(), c, dir, p, func(string) error { return nil }); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
}
func TestDownloadRejectsCorruptionAndBadRange(t *testing.T) {
	for _, badRange := range []bool{false, true} {
		t.Run(fmt.Sprint(badRange), func(t *testing.T) {
			p := profile("right")
			dir := t.TempDir()
			path := filepath.Join(dir, p.SHA256+".ipsw")
			code := 200
			headers := http.Header{}
			body := "wrong"
			if badRange {
				if err := os.WriteFile(path+".partial", []byte("r"), 0600); err != nil {
					t.Fatal(err)
				}
				code = 206
				headers.Set("Content-Range", "bytes 0-3/5")
				body = "ight"
			}
			c := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: code, Header: headers, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
			})}
			if _, err := Download(context.Background(), c, dir, p, func(string) error { return nil }); err == nil {
				t.Fatal("accepted corrupt restore image")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("published unverified IPSW")
			}
		})
	}
}
func TestDownloadRejectsUntrustedURLAndSymlink(t *testing.T) {
	p := profile("right")
	p.URL = "http://127.0.0.1/admin.ipsw"
	if _, err := Download(context.Background(), http.DefaultClient, t.TempDir(), p, func(string) error { return nil }); err == nil {
		t.Fatal("accepted arbitrary URL")
	}
	p = profile("right")
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("right"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, p.SHA256+".ipsw")); err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), http.DefaultClient, dir, p, func(string) error { return nil }); err == nil {
		t.Fatal("accepted cache symlink")
	}
}
