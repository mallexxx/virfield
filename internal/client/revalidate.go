package client

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

// SignRevalidation reads only the caller-selected existing identity. It cannot
// create/replace a key or sign a general SSH challenge supplied by a server.
func SignRevalidation(l domain.Lease, dir, key string) (domain.RevalidateRequest, error) {
	var empty domain.RevalidateRequest
	if l.SSH == nil || !domain.ValidName(l.ID) || !domain.ValidName(l.VMName) {
		return empty, fmt.Errorf("lease has no verified caller identity")
	}
	path := filepath.Join(dir, "id_ed25519")
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return empty, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return empty, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 32768 || !ok || int(owner.Uid) != os.Getuid() {
		return empty, fmt.Errorf("existing caller key must be an owner-only regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 32769))
	if err != nil {
		return empty, err
	}
	if len(data) > 32768 {
		return empty, fmt.Errorf("existing caller key exceeds size limit")
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return empty, fmt.Errorf("cannot parse existing caller key")
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 || ssh.FingerprintSHA256(signer.PublicKey()) != l.SSH.ClientKeyFingerprint {
		return empty, fmt.Errorf("existing caller key does not belong to this lease")
	}
	signature, err := signer.Sign(rand.Reader, domain.RevalidationMessage(l.ID, l.VMName, key))
	if err != nil {
		return empty, err
	}
	r := domain.RevalidateRequest{VMName: l.VMName, CallerProof: base64.StdEncoding.EncodeToString(ssh.Marshal(signature))}
	if err := domain.VerifyRevalidationProof(l, key, r); err != nil {
		return empty, err
	}
	return r, nil
}
