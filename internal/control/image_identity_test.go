package control

import (
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

// The v2 Lume manifest identity must not change when Tart fields are added.
func TestLumeFingerprintIsStable(t *testing.T) {
	p := domain.ImageProfile{MacOS: "13.6", Build: "22G120", Security: "sip-disabled", Provision: "developer-v1", DisableSIP: true, URL: "https://updates.cdn-apple.com/x.ipsw", SHA256: "9bf095739b8b2d5ebd20f7e8de938f10bc449f9843de21c4a41ae54d73526728", Size: 12893555341, Xcode: &domain.XcodeRelease{Version: "15.2", Build: "15C500b", Requires: "13.5", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_15.2/Xcode_15.2.xip", SHA1: "3f1e6943264ba640f83e655768df439902acc406"}}
	if got := imageFingerprint(&p); got != "cb5a8a977aaceed301cc350945d4ba736c7c4aab3ea0124f764e70183add2426" {
		t.Fatalf("Lume fingerprint changed: %s", got)
	}
}

func TestTartUpstreamIsPartOfTheFingerprint(t *testing.T) {
	a := domain.ImageProfile{MacOS: "13.7.4", Build: "22H420", Tart: &domain.TartSource{Upstream: "ghcr.io/a/b@sha256:1"}}
	b := a
	b.Tart = &domain.TartSource{Upstream: "ghcr.io/a/b@sha256:2"}
	if imageFingerprint(&a) == imageFingerprint(&b) {
		t.Fatal("different upstream digests share an image identity")
	}
}
