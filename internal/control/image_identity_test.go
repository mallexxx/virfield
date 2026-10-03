package control

import (
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestImageFingerprintIgnoresCatalogOnlyXcodeState(t *testing.T) {
	x := domain.XcodeRelease{Version: "13.4.1", Build: "13F100", Requires: "12.0", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_13.4.1.xip", SHA1: strings.Repeat("a", 40)}
	p := domain.ImageProfile{MacOS: "12.6", Build: "21G115", URL: "https://updates.cdn-apple.com/restore.ipsw", SHA256: strings.Repeat("b", 64), Size: 123, Xcode: &x, Provision: "developer-v1"}
	want := fingerprint(&p) // The pre-existing v2 identity for a clean catalog release.
	if got := imageFingerprint(&p); got != want {
		t.Fatal(got, want)
	}
	x.LocalState = "installed"
	x.InstalledImages = []string{"another-template"}
	if got := imageFingerprint(&p); got != want {
		t.Fatal("catalog metadata changed manifest identity", got, want)
	}
}
