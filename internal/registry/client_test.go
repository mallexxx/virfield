package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func manifestFixture(modern bool) []byte {
	d := "sha256:" + strings.Repeat("a", 64)
	config, disk, nvram := "application/vnd.oci.image.config.v1+json", "application/octet-stream+lz4", "application/octet-stream"
	if modern {
		config, disk, nvram = "application/vnd.trycua.lume.config.v1+json", "application/vnd.trycua.lume.disk.v1", "application/vnd.trycua.lume.nvram.v1"
	}
	layers := []descriptor{{MediaType: disk, Digest: d, Size: 100}, {MediaType: nvram, Digest: d, Size: 50}}
	if !modern {
		layers = append(layers, descriptor{MediaType: config, Digest: d, Size: 10})
	}
	b, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": descriptor{MediaType: config, Digest: d, Size: 10}, "layers": layers})
	return b
}
func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}
func TestResolvePinsManifestAndKeepsCredentialsPrivate(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "oci"}[modern], func(t *testing.T) {
			tokenFile := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenFile, []byte("test-private-secret\n"), 0600); err != nil {
				t.Fatal(err)
			}
			body := manifestFixture(modern)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/token" {
					user, pass, ok := r.BasicAuth()
					if !ok || user != "user" || pass != "test-private-secret" {
						t.Error("missing auth")
					}
					if r.URL.Query().Get("scope") != "repository:team/vm:pull" {
						t.Error("bad scope")
					}
					_, _ = w.Write([]byte(`{"token":"scoped-token"}`))
					return
				}
				if r.URL.Path != "/v2/team/vm/manifests/stable" || r.Header.Get("Authorization") != "Bearer scoped-token" {
					t.Error("bad manifest request")
				}
				sum := sha256.Sum256(body)
				w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(sum[:]))
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, err := New([]domain.RegistrySource{{ID: "team", Organization: "team", Username: "user", TokenFile: tokenFile, AllowPush: true}})
			if err != nil {
				t.Fatal(err)
			}
			client.base = server.URL
			ref, err := client.Resolve(context.Background(), domain.RegistryResolveRequest{Source: "team", Repository: "vm", Tag: "stable"})
			if err != nil || !domain.ValidRegistryDigest(ref.Digest) || calls != 2 {
				t.Fatal(ref, err, calls)
			}
			sources, _ := json.Marshal(client.Sources())
			if strings.Contains(string(sources), "user") || strings.Contains(string(sources), tokenFile) || strings.Contains(string(sources), "secret") {
				t.Fatal("credential metadata leaked")
			}
		})
	}
}
func TestResolveRejectsUntrustedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, body, digest, want string
		status                   int
	}{
		{name: "digest", body: string(manifestFixture(true)), digest: "sha256:" + strings.Repeat("b", 64), want: "registry_digest_mismatch"},
		{name: "index", body: `{"schemaVersion":2,"manifests":[]}`, want: "registry_invalid"},
		{name: "container", body: `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","size":1,"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`, want: "registry_format_unsupported"},
		{name: "oversized", body: strings.Repeat("x", (4<<20)+1), want: "registry_invalid"},
		{name: "denied", body: "secret upstream message", status: 403, want: "registry_auth_required"},
		{name: "absent", status: 404, want: "registry_not_found"},
		{name: "redirect", status: 302, want: "registry_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redirected := 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++ }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = w.Write([]byte(`{"token":"private-bearer"}`))
					return
				}
				w.Header().Set("Docker-Content-Digest", tc.digest)
				w.Header().Set("Location", target.URL)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			c, _ := New([]domain.RegistrySource{{ID: "test", Organization: "team"}})
			c.base = server.URL
			_, err := c.Resolve(context.Background(), domain.RegistryResolveRequest{Source: "test", Repository: "vm", Tag: "tag"})
			assertCode(t, err, tc.want)
			if strings.Contains(err.Error(), "private-bearer") || strings.Contains(err.Error(), "secret upstream") || redirected != 0 {
				t.Fatal("credential or redirect boundary breached")
			}
		})
	}
}
func TestCredentialsAndSourceValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	s := domain.RegistrySource{ID: "team", Organization: "team", Username: "user", TokenFile: path, AllowPush: true}
	for _, tc := range []struct {
		body  string
		mode  os.FileMode
		valid bool
	}{{"value", 0600, true}, {"value", 0644, false}, {"", 0600, false}, {"a\nb", 0600, false}, {strings.Repeat("x", 4097), 0600, false}} {
		if err := os.WriteFile(path, []byte(tc.body), tc.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, tc.mode); err != nil {
			t.Fatal(err)
		}
		_, err := Credentials(s)
		if (err == nil) != tc.valid {
			t.Fatalf("mode %o valid %v: %v", tc.mode, tc.valid, err)
		}
	}
	if _, err := New([]domain.RegistrySource{s, s}); err == nil {
		t.Fatal("duplicate accepted")
	}
	c, _ := New([]domain.RegistrySource{{ID: "public", Organization: "team"}})
	_, err := c.PushTarget("public")
	assertCode(t, err, "registry_push_disabled")
}
