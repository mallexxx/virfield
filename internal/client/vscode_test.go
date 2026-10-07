package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

func TestWriteVSCodeConfig(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(root, "lease identity")
	if err := GenerateIdentity(identity); err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(filepath.Join(identity, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	lease := domain.Lease{ID: "lease-vscode", State: "ready", IP: "192.168.64.12", SSH: &domain.SSHConnection{
		User: "lume", Port: 22, HostKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey())), ClientKeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
	}}
	if err := WriteSSHConfig(identity, lease); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "apple-browsers")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	files, err := WriteVSCodeConfig(workspace, identity, lease)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := os.ReadFile(files.Adapter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(adapter), "ssh") || !strings.Contains(string(adapter), "xcrun lldb-dap") {
		t.Fatal("adapter does not run host lldb-dap")
	}
	prepare, err := os.ReadFile(files.Prepare)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mount_nfs", "dsc_extractor.bundle", "/System/Library/dyld", "debugserver", "-L", "127.0.0.1"} {
		if !strings.Contains(string(prepare), want) {
			t.Fatalf("prepare helper missing %q", want)
		}
	}
	for _, path := range []string{files.Launch, files.Tasks, files.Settings} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(b, &value); err != nil {
			t.Fatalf("invalid JSON in %s: %v", path, err)
		}
	}
	tasks, err := os.ReadFile(files.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Virfield: Sync artifacts", "Virfield: Build", "Virfield: Test", "swift test", "--delete"} {
		if !strings.Contains(string(tasks), want) {
			t.Fatalf("tasks missing %q", want)
		}
	}
	if _, err := WriteVSCodeConfig(workspace, identity, lease); err == nil {
		t.Fatal("overwrote existing VS Code configuration")
	}
}

func TestWriteVSCodeConfigRejectsChangedPin(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(root, "identity")
	if err := GenerateIdentity(identity); err != nil {
		t.Fatal(err)
	}
	private, _ := os.ReadFile(filepath.Join(identity, "id_ed25519"))
	signer, _ := ssh.ParsePrivateKey(private)
	lease := domain.Lease{ID: "lease-vscode", State: "ready", IP: "192.168.64.12", SSH: &domain.SSHConnection{
		User: "lume", Port: 22, HostKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey())), ClientKeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
	}}
	if err := WriteSSHConfig(identity, lease); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identity, "config"), []byte("Host virfield\n"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "package")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteVSCodeConfig(workspace, identity, lease); err == nil {
		t.Fatal("accepted changed SSH configuration")
	}
}

func TestWriteVSCodeConfigUsesDuckDuckGoWorkspace(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(root, "identity")
	if err := GenerateIdentity(identity); err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(filepath.Join(identity, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	lease := domain.Lease{ID: "lease-duck", VMName: "duck-vm", State: "ready", IP: "192.168.64.13", Source: &domain.Template{Image: &domain.ImageProfile{MacOS: "27.0", Build: "26A428"}}, SSH: &domain.SSHConnection{
		User: "lume", Port: 22, HostKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey())), ClientKeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
	}}
	if err := WriteSSHConfig(identity, lease); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "spm-browser-tests")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	sourceWorkspace := `{
  // The real file is JSONC.
  "folders": [{"path": "."}],
  "settings": {"swift.buildArguments": ["--build-system", "swiftbuild"]},
  "extensions": {"recommendations": ["swiftlang.swift-vscode"]},
  "tasks": {"version": "2.0.0", "tasks": []},
  "launch": {"version": "0.2.0", "configurations": []}
}`
	if err := os.WriteFile(filepath.Join(workspace, "DuckDuckGo-macOS.code-workspace"), []byte(sourceWorkspace), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := WriteVSCodeConfig(workspace, identity, lease)
	if err != nil {
		t.Fatal(err)
	}
	if files.Workspace == "" || files.Launch != "" || files.Tasks != "" || files.Settings != "" {
		t.Fatalf("unexpected DuckDuckGo outputs: %+v", files)
	}
	b, err := os.ReadFile(files.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	var generated map[string]any
	if err := json.Unmarshal(b, &generated); err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{
		"DuckDuckGo — Virfield VM: duck-vm [lease-duck]",
		"Virfield: Build DuckDuckGo",
		"Virfield: Sync artifacts",
		"Virfield: Prepare DuckDuckGo debug",
		"Virfield: Open console",
		"Virfield: Open VM terminal",
		"Virfield: Test",
		"--build-system",
		"swiftbuild",
		"DuckDuckGoBrowserDynamic",
		"make-host-app.sh --force",
		"codesign --force --deep --sign -",
		"DuckDuckGo.app/Contents/MacOS/DuckDuckGo",
		"target.memory-module-load-level minimal",
		"macOS DeviceSupport",
		"27.0 (26A428)/Symbols/usr/lib",
		"gdb-remote 127.0.0.1:",
		"inferior-shared-cache-only",
		"llvm-vs-code-extensions.lldb-dap",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("DuckDuckGo workspace missing %q", want)
		}
	}
	folders := generated["folders"].([]any)
	if folders[0].(map[string]any)["path"] != ".." {
		t.Fatal("generated workspace does not point back to the worktree")
	}
	prepare, err := os.ReadFile(files.Prepare)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prepare), "mount_nfs") || !strings.Contains(string(prepare), "debugserver") {
		t.Fatal("DuckDuckGo prepare helper does not mount dyld cache and start debugserver")
	}
}

func TestWriteVSCodeConfigsOffersEachReadyVM(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "spm-browser-tests")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "DuckDuckGo-macOS.code-workspace"), []byte(`{
  "folders": [{"path": "."}],
  "settings": {},
  "tasks": {"version": "2.0.0", "tasks": []},
  "launch": {"version": "0.2.0", "configurations": []}
}`), 0600); err != nil {
		t.Fatal(err)
	}
	targets := make([]VSCodeTarget, 0, 2)
	for i, name := range []string{"ventura-a", "ventura-b"} {
		identity := filepath.Join(root, "identity-"+name)
		if err := GenerateIdentity(identity); err != nil {
			t.Fatal(err)
		}
		private, err := os.ReadFile(filepath.Join(identity, "id_ed25519"))
		if err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.ParsePrivateKey(private)
		if err != nil {
			t.Fatal(err)
		}
		lease := domain.Lease{
			ID: "lease-" + name, VMName: name, State: "ready", IP: "192.168.64." + string(rune('2'+i)),
			SSH: &domain.SSHConnection{User: "lume", Port: 22, HostKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey())), ClientKeyFingerprint: ssh.FingerprintSHA256(signer.PublicKey())},
		}
		if err := WriteSSHConfig(identity, lease); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, VSCodeTarget{IdentityDir: identity, Lease: lease})
	}
	files, err := WriteVSCodeConfigs(workspace, targets)
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Helpers) != 2 {
		t.Fatalf("helpers = %v", files.Helpers)
	}
	b, err := os.ReadFile(files.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{
		"DuckDuckGo — Virfield VM: ventura-a [lease-ventura-a]",
		"DuckDuckGo — Virfield VM: ventura-b [lease-ventura-b]",
		"Virfield: Open VM terminal — ventura-a [lease-ventura-a]",
		"Virfield: Open VM terminal — ventura-b [lease-ventura-b]",
		"Virfield: Open console",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("multi-VM workspace missing %q", want)
		}
	}
}

func TestDeviceSupportForLeaseFindsBuildWhenManifestVersionIsEmpty(t *testing.T) {
	home := t.TempDir()
	symbols := filepath.Join(home, "Library", "Developer", "Xcode", "macOS DeviceSupport", "27.0 (26A428)", "Symbols")
	libobjc := filepath.Join(symbols, "usr", "lib", "libobjc.A.dylib")
	if err := os.MkdirAll(filepath.Dir(libobjc), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libobjc, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	lease := domain.Lease{Source: &domain.Template{Image: &domain.ImageProfile{Build: "26A428"}}}
	if got := deviceSupportForLease(home, lease, 43001); got != symbols {
		t.Fatalf("DeviceSupport path = %q, want %q", got, symbols)
	}
}
