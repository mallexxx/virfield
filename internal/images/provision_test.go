package images

import "testing"

func TestUIProfileRejectsIncompatibleXcodeBeforeGuestChanges(t *testing.T) {
	for _, tc := range []struct {
		version    string
		compatible bool
	}{
		{"Xcode 26.6\nBuild version 17F113\n", false},
		{"Xcode 27.2\nBuild version 27B5019j\n", true},
		{"Xcode 27.0\nBuild version 27A123\n", true},
		{"xcode-select: error: tool not found", false},
		{"Xcode invalid", false},
	} {
		if got := compatibleXcode(tc.version); got != tc.compatible {
			t.Errorf("%q: compatibility %v", tc.version, got)
		}
	}
}
