package images

import (
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
