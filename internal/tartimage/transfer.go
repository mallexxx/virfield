package tartimage

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/verifiedcopy"
)

// TransferImage preserves the private image mapping and qualification evidence.
// It never boots, reprovisions, resets credentials or uploads a managed golden.
func (b *Builder) TransferImage(ctx context.Context, source, destination domain.Lease) (string, error) {
	runtime, ok := b.config.Runtime.(interface {
		CopyImage(context.Context, domain.Lease, domain.Lease) (string, error)
	})
	if !ok || destination.Source == nil || destination.Source.Image == nil || destination.Source.Image.Tart == nil {
		return "", domain.Err("image_transfer_unsupported", "Runtime has no verified local-copy implementation")
	}
	src, dst := b.folder(source), b.folder(destination)
	credentials, err := guestssh.LoadCredentials(filepath.Join(src, "credentials.json"))
	if err != nil || !credentials.Secured || len(credentials.HostKey) == 0 {
		return "", domain.Err("image_credentials_missing", "Source image identity is missing or unverified")
	}
	verification, err := os.ReadFile(filepath.Join(src, "verification.json"))
	if err != nil {
		return "", domain.Err("image_transfer_mismatch", "Source qualification evidence is missing")
	}
	var evidence struct {
		MacOS     string               `json:"macos"`
		Build     string               `json:"build"`
		Upstream  string               `json:"upstream"`
		Recipe    string               `json:"recipe_sha256"`
		Scoped    bool                 `json:"scoped_image_ssh"`
		Portable  bool                 `json:"portable"`
		GuestUser string               `json:"guest_user"`
		Security  string               `json:"security"`
		Provision string               `json:"provision"`
		Desktop   bool                 `json:"desktop"`
		Xcode     *domain.XcodeRelease `json:"xcode"`
	}
	if err := json.Unmarshal(verification, &evidence); err != nil {
		return "", domain.Err("image_transfer_mismatch", "Source qualification evidence is invalid")
	}
	recipe, err := os.ReadFile(filepath.Join(src, "recipe.sha256"))
	p := destination.Source.Image
	digestBytes, digestErr := hex.DecodeString(string(recipe))
	if err != nil || digestErr != nil || len(digestBytes) != 32 || evidence.Recipe != string(recipe) || evidence.MacOS != p.MacOS || evidence.Build != p.Build || evidence.Upstream != p.Tart.Upstream || !evidence.Scoped || evidence.Portable || evidence.GuestUser != source.GuestUser || !evidence.Desktop || evidence.Security != p.Security || evidence.Provision != p.Provision || p.Xcode != nil && !samePinnedXcode(evidence.Xcode, p.Xcode) {
		return "", domain.Err("image_transfer_mismatch", "Source recipe, qualification or private identity does not match the verified image")
	}
	if err := os.Mkdir(dst, 0700); err != nil {
		return "", domain.Err("image_exists", "Destination private image directory already exists or cannot be created")
	}
	// Only the private identity and immutable qualification inputs/evidence are
	// carried forward. Historical logs retain their original source binding.
	for _, name := range []string{"credentials.json", "recipe.sha256", "verification.json"} {
		file, err := verifiedcopy.Open(filepath.Join(src, name))
		if err != nil {
			return "", err
		}
		_, err = verifiedcopy.File(ctx, file, filepath.Join(dst, name))
		file.Close()
		if err != nil {
			return "", err
		}
	}
	if err := verifiedcopy.SyncDirectory(dst); err != nil {
		return "", err
	}
	digest, err := runtime.CopyImage(ctx, source, destination)
	if err != nil {
		return "", err
	}
	receipt := struct {
		Source          string `json:"source_image_id"`
		Manifest        string `json:"image_manifest"`
		Content         string `json:"content_sha256"`
		SourcePreserved bool   `json:"source_preserved"`
	}{source.ID, source.ImageManifest, digest, true}
	raw, _ := json.Marshal(receipt)
	if err := os.WriteFile(filepath.Join(dst, "transfer.json"), raw, 0600); err != nil {
		return "", err
	}
	if err := verifiedcopy.SyncDirectory(dst); err != nil {
		return "", err
	}
	return digest, nil
}

func samePinnedXcode(a, b *domain.XcodeRelease) bool {
	return a != nil && b != nil && a.Version == b.Version && a.Build == b.Build && a.Requires == b.Requires && a.URL == b.URL && a.SHA1 == b.SHA1
}

func (b *Builder) DiscardImageCopy(ctx context.Context, l domain.Lease) error {
	runtime, ok := b.config.Runtime.(interface {
		DiscardImageCopy(context.Context, domain.Lease) error
	})
	if !ok {
		return domain.Err("image_transfer_unsupported", "Runtime cannot inspect and discard an owned partial copy")
	}
	return runtime.DiscardImageCopy(ctx, l)
}
