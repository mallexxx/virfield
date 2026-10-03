package images

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestDetachTransferRootMovesStaleFilesOutOfHotPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "transfer")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stale"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	detached, err := detachTransferRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if detached == "" {
		t.Fatal("expected stale transfer root to be detached")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("stale transfer root stayed in the hot path", err)
	}
	if _, err := os.Stat(filepath.Join(detached, "stale")); err != nil {
		t.Fatal("stale transfer file was not moved to the detached cleanup path", err)
	}
}

func TestSharedXcodeInstallScriptUsesVirtioFSMount(t *testing.T) {
	script := sharedXcodeInstallScript("Xcode.app")
	for _, want := range []string{
		"/Volumes/My Shared Files/Xcode.app",
		"/usr/bin/ditto \"$src\" \"$staged\"",
		"sudo /bin/mv \"$staged\" /Applications/Xcode.app",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %q in:\n%s", want, script)
		}
	}
	if strings.Contains(script, "tar -xpf") || strings.Contains(script, "scp ") {
		t.Fatal("SSH streaming transfer leaked back into VirtioFS install script")
	}
}

func TestLumeRunSharedArgsUseNativeSharedDirContract(t *testing.T) {
	got := lumeRunSharedArgs(domain.Lease{VMName: "vf-test", Location: "home"}, "/tmp/source")
	want := []string{"run", "vf-test", "--storage", "home", "--no-display", "--shared-dir", "/tmp/source"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v", got)
	}
}

func TestGuestSupportsVirtioFSRequiresVenturaOrNewer(t *testing.T) {
	for _, tc := range []struct {
		macos string
		want  bool
	}{
		{"12.6", false},
		{"13", true},
		{"15.2", true},
	} {
		if got := guestSupportsVirtioFS(tc.macos); got != tc.want {
			t.Fatalf("guestSupportsVirtioFS(%q) = %v", tc.macos, got)
		}
	}
}

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
