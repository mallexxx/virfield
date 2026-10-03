package images

import (
	"errors"
	"io"
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

func TestOlderGuestWithXcodeGetsExpansionSpaceAtCreation(t *testing.T) {
	for _, tc := range []struct {
		macos string
		xcode bool
		want  string
	}{
		{"12.6", true, "120GB"},
		{"11.7", true, "120GB"},
		{"13.0", true, "80GB"},
		{"12.6", false, "80GB"},
	} {
		p := domain.ImageProfile{MacOS: tc.macos}
		if tc.xcode {
			p.Xcode = &domain.XcodeRelease{}
		}
		if got := imageDiskSize(p); got != tc.want {
			t.Fatalf("macOS %s xcode=%v: disk size %s, want %s", tc.macos, tc.xcode, got, tc.want)
		}
	}
}

func TestMontereyXIPTransferChecksDigestAndSignatureBeforeReplacingApp(t *testing.T) {
	receive := sshXcodeReceiveScript("0123456789abcdef0123456789abcdef01234567")
	expand := sshXcodeExpandScript("13.4.1", "13F100")
	bundle := sshXcodeBundleInstallScript("13.4.1", "13F100")
	for _, want := range []string{"/bin/cat > \"$partial\"", "/usr/bin/shasum -a 1", "0123456789abcdef0123456789abcdef01234567", "trap '/bin/rm -f \"$partial\"'"} {
		if !strings.Contains(receive, want) {
			t.Fatalf("receive script missing %q", want)
		}
	}
	for _, want := range []string{"/usr/bin/xip --expand", "13.4.1", "13F100", "/usr/bin/codesign --verify --deep --strict", "sudo /bin/mv \"$app\" /Applications/Xcode.app"} {
		if !strings.Contains(expand, want) {
			t.Fatalf("expand script missing %q", want)
		}
	}
	if strings.Index(expand, "/usr/bin/codesign --verify") > strings.Index(expand, "sudo /bin/rm -rf /Applications/Xcode.app") {
		t.Fatal("existing Xcode is replaced before staged signature verification")
	}
	for _, want := range []string{"COPYFILE_DISABLE=1 /usr/bin/tar -xpf -", "13.4.1", "13F100", "/usr/bin/codesign --verify --deep --strict", "sudo /bin/mv \"$app\" /Applications/Xcode.app"} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("bundle script missing %q", want)
		}
	}
	if strings.Index(bundle, "/usr/bin/codesign --verify") > strings.Index(bundle, "sudo /bin/rm -rf /Applications/Xcode.app") {
		t.Fatal("bundle fallback replaces Xcode before staged signature verification")
	}
	if !xipOutOfSpace("xip: error: The archive can't be expanded because the selected volume doesn't have enough free space.") || xipOutOfSpace("xip: invalid signature") {
		t.Fatal("XIP fallback must only run for exhausted guest storage")
	}
}

func TestXcodeProgressReaderReportsAndStopsOnProgressError(t *testing.T) {
	stop := errors.New("stop")
	calls := 0
	r := &xcodeProgressReader{reader: strings.NewReader("archive"), total: 7, progress: func(s string) error {
		calls++
		if s != "Transferring Xcode XIP to guest: 100%" {
			t.Fatal(s)
		}
		return stop
	}}
	buf := make([]byte, 7)
	if n, err := r.Read(buf); n != 0 || !errors.Is(err, stop) {
		t.Fatalf("read = %d, %v", n, err)
	}
	if n, err := r.Read(buf); n != 0 || !errors.Is(err, stop) {
		t.Fatalf("subsequent read = %d, %v", n, err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, stop) {
		t.Fatal(err)
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
