package domain

import (
	"encoding/base64"

	"golang.org/x/crypto/ssh"
)

type RevalidateRequest struct {
	VMName      string `json:"vm_name"`
	CallerProof string `json:"caller_proof"`
}

func RevalidationMessage(id, vmName, key string) []byte {
	return []byte("virfield-lease-revalidation-v1\n" + id + "\n" + vmName + "\n" + key + "\n")
}

func (r RevalidateRequest) Validate() error {
	if !ValidName(r.VMName) || len(r.CallerProof) > 1024 || r.CallerProof == "" {
		return Err("invalid_request", "Revalidation requires the exact VM name and a locally signed caller proof")
	}
	return nil
}

func VerifyRevalidationProof(l Lease, key string, r RevalidateRequest) error {
	if l.SSH == nil || l.VMName != r.VMName {
		return Err("forbidden", "Revalidation proof does not match this lease")
	}
	public, err := CanonicalPublicKey(l.SSHPublicKey)
	if err != nil {
		return Err("forbidden", "Lease caller identity is unavailable")
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(public))
	if err != nil || ssh.FingerprintSHA256(pub) != l.SSH.ClientKeyFingerprint {
		return Err("forbidden", "Lease caller identity does not match its verified fingerprint")
	}
	data, err := base64.StdEncoding.DecodeString(r.CallerProof)
	if err != nil || base64.StdEncoding.EncodeToString(data) != r.CallerProof {
		return Err("forbidden", "Invalid caller proof")
	}
	var signature ssh.Signature
	if ssh.Unmarshal(data, &signature) != nil || signature.Format != ssh.KeyAlgoED25519 || len(signature.Blob) != 64 || len(signature.Rest) != 0 {
		return Err("forbidden", "Invalid caller proof")
	}
	if err := pub.Verify(RevalidationMessage(l.ID, r.VMName, key), &signature); err != nil {
		return Err("forbidden", "Caller proof does not authorize this exact revalidation request")
	}
	return nil
}

type Revalidation struct {
	Candidate     VM                `json:"candidate"`
	Request       RevalidateRequest `json:"request"`
	Key           string            `json:"idempotency_key"`
	Baseline      RuntimeIdentity   `json:"baseline"`
	ImageManifest string            `json:"image_manifest"`
}

func (r RuntimeIdentity) Matches(vm VM) bool {
	return r.Complete() && r.RuntimeID == vm.RuntimeID && r.VMName == vm.Name && r.Location == vm.Location && r.Provider == vm.Provider && r.HostID == vm.HostID && r.ContainerID == vm.ContainerID && r.OS == vm.OS && r.Resources == vm.Resources
}

func (r RuntimeIdentity) Complete() bool {
	return r.RuntimeID != "" && r.VMName != "" && r.Location != "" && r.Provider != "" && r.HostID != "" && r.ContainerID != "" && r.OS != "" && r.Resources.CPU > 0 && r.Resources.MemoryBytes > 0 && r.Resources.DiskBytes > 0
}

func (r Revalidation) Validate(l Lease) error {
	if err := r.Request.Validate(); err != nil {
		return err
	}
	if l.VerifiedRuntime == nil || !l.VerifiedRuntime.Matches(r.Candidate) {
		return Err("guest_identity_failed", "Lease runtime baseline changed")
	}
	if r.Baseline.RuntimeID == "" || r.Candidate.RuntimeID == "" || !r.Baseline.Matches(r.Candidate) {
		return Err("guest_identity_failed", "Runtime identity changed; the existing VM is preserved for inspection")
	}
	if r.ImageManifest == "" {
		return Err("guest_identity_failed", "Persisted revalidation has no image provenance")
	}
	return VerifyRevalidationProof(l, r.Key, r.Request)
}
