package qualify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeviceSupportPathUsesXcodeConvention(t *testing.T) {
	got, err := deviceSupportPath("/Users/tester", "27.0", "26A428")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/Users/tester", "Library", "Developer", "Xcode", "macOS DeviceSupport", "27.0 (26A428)", "Symbols")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if !strings.Contains(dscExtractorSource, "argc != 4") || !strings.Contains(dscExtractorSource, "dyld_shared_cache_extract_dylibs_progress") {
		t.Fatal("embedded extractor does not validate arguments or load Apple's extraction function")
	}
}

func TestDeviceSupportPathRejectsUntrustedComponents(t *testing.T) {
	for _, test := range []struct{ home, version, build string }{
		{"relative", "27.0", "26A428"},
		{"/Users/tester", "27.0/../../tmp", "26A428"},
		{"/Users/tester", "27.0", "../../tmp"},
	} {
		if _, err := deviceSupportPath(test.home, test.version, test.build); err == nil {
			t.Fatalf("accepted unsafe DeviceSupport components: %#v", test)
		}
	}
}

// Xcode ships dsc_extractor.bundle as a Mach-O bundle file, not a directory.
func TestExtractorBundleIsARegularFile(t *testing.T) {
	developer := t.TempDir()
	lib := filepath.Join(developer, "Platforms", "iPhoneOS.platform", "usr", "lib")
	if err := os.MkdirAll(lib, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := extractorBundle(developer); err == nil {
		t.Fatal("accepted a missing extractor")
	}
	bundle := filepath.Join(lib, "dsc_extractor.bundle")
	if err := os.WriteFile(bundle, []byte("mach-o"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := extractorBundle(developer); err != nil || got != bundle {
		t.Fatal(got, err)
	}
}
