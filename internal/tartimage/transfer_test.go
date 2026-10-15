package tartimage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"golang.org/x/crypto/ssh"
)

type copyRuntime struct {
	fakeRuntime
	calls int
}

func (r *copyRuntime) CopyImage(context.Context, domain.Lease, domain.Lease) (string, error) {
	r.calls++
	return strings.Repeat("a", 64), nil
}

func TestTransferPreservesPrivateIdentityAndQualifiedProvenance(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserved", true: "mismatched-evidence"}[mismatch], func(t *testing.T) {
			root := t.TempDir()
			runtime := &copyRuntime{}
			b := New(Config{StateDir: root, Runtime: runtime})
			source := domain.Lease{ID: "image-source", Purpose: "image", GuestUser: "admin", ImageManifest: "qualified"}
			p := domain.ImageProfile{Tart: &domain.TartSource{Upstream: "ghcr.io/test/macos@sha256:" + strings.Repeat("b", 64)}, MacOS: "12.7.6", Build: "21H1320", Provision: "base-v1"}
			dest := domain.Lease{ID: "image-dest", Purpose: "image", Source: &domain.Template{Image: &p}}
			os.MkdirAll(b.folder(source), 0700)
			manager := &guestssh.Manager{Dir: root}
			credentials, err := manager.CredentialsFor(source)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.ParsePrivateKey(credentials.Private)
			if err != nil {
				t.Fatal(err)
			}
			credentials.Secured, credentials.HostKey = true, signer.PublicKey().Marshal()
			if err := guestssh.SaveCredentials(manager.CredentialPath(source), credentials); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(b.folder(source), "recipe.sha256"), []byte(strings.Repeat("c", 64)), 0600)
			evidence := map[string]any{"macos": p.MacOS, "build": p.Build, "upstream": p.Tart.Upstream, "provision": p.Provision, "recipe_sha256": strings.Repeat("c", 64), "scoped_image_ssh": true, "guest_user": "admin", "desktop": true}
			if mismatch {
				evidence["build"] = "foreign-build"
			}
			raw, _ := json.Marshal(evidence)
			os.WriteFile(filepath.Join(b.folder(source), "verification.json"), raw, 0600)
			digest, err := b.TransferImage(context.Background(), source, dest)
			if mismatch {
				if err == nil || runtime.calls != 0 {
					t.Fatal("copied unqualified source", err)
				}
				return
			}
			if err != nil || len(digest) != 64 || runtime.calls != 1 {
				t.Fatal(digest, runtime.calls, err)
			}
			for _, name := range []string{"credentials.json", "recipe.sha256", "verification.json"} {
				a, _ := os.ReadFile(filepath.Join(b.folder(source), name))
				z, e := os.ReadFile(filepath.Join(b.folder(dest), name))
				if e != nil || string(a) != string(z) {
					t.Fatal("lost private identity/provenance", name, e)
				}
			}
			if _, err := b.TransferImage(context.Background(), source, dest); err == nil || runtime.calls != 1 {
				t.Fatal("overwrote private destination")
			}
		})
	}
}
