package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

func TestRevalidationProofBoundToExistingKeyLeaseNameAndRequest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")
	if err := GenerateIdentity(dir); err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(dir, "id_ed25519")
	before, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(filepath.Join(dir, "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(public)
	if err != nil {
		t.Fatal(err)
	}
	l := domain.Lease{ID: "lease-revalidate", VMName: "vf-revalidate", SSHPublicKey: string(public), SSH: &domain.SSHConnection{ClientKeyFingerprint: ssh.FingerprintSHA256(pub)}}
	r, err := SignRevalidation(l, dir, "request-revalidate")
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.VerifyRevalidationProof(l, "request-revalidate", r); err != nil {
		t.Fatal(err)
	}
	if err := domain.VerifyRevalidationProof(l, "another-request", r); err == nil {
		t.Fatal("Proof reused for another request")
	}
	changed := l
	changed.ID = "lease-other"
	if err := domain.VerifyRevalidationProof(changed, "request-revalidate", r); err == nil {
		t.Fatal("Proof reused for another lease")
	}
	changed = l
	changed.VMName = "vf-other"
	if err := domain.VerifyRevalidationProof(changed, "request-revalidate", r); err == nil {
		t.Fatal("Proof reused for another VM")
	}
	again, err := SignRevalidation(l, dir, "request-revalidate")
	if err != nil || again != r {
		t.Fatal("Retry changed the signed request", err)
	}
	after, _ := os.ReadFile(privatePath)
	if string(after) != string(before) {
		t.Fatal("Caller private key was modified")
	}
	if err := os.Chmod(privatePath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := SignRevalidation(l, dir, "request-revalidate"); err == nil {
		t.Fatal("World-readable private key accepted")
	}
	if err := os.Chmod(privatePath, 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(r)
	if string(data) == "" || len(data) > 1024 {
		t.Fatal("Proof exceeds bounded public request", len(data))
	}
	if err := os.Remove(privatePath); err != nil {
		t.Fatal(err)
	}
	if _, err := SignRevalidation(l, dir, "request-revalidate"); err == nil {
		t.Fatal("Missing key was silently recreated")
	}
	if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
		t.Fatal("Revalidation created an identity")
	}
	if err := os.Symlink(filepath.Join(dir, "id_ed25519.pub"), privatePath); err != nil {
		t.Fatal(err)
	}
	if _, err := SignRevalidation(l, dir, "request-revalidate"); err == nil {
		t.Fatal("Symlink accepted as private key")
	}
}
