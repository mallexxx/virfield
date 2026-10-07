package client

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// VSCodeFiles are the workspace-local files created for a pinned lease.
type VSCodeFiles struct {
	Workspace string   `json:"workspace,omitempty"`
	Launch    string   `json:"launch,omitempty"`
	Tasks     string   `json:"tasks,omitempty"`
	Settings  string   `json:"settings,omitempty"`
	Adapter   string   `json:"adapter"`
	Prepare   string   `json:"prepare"`
	Helpers   []string `json:"helpers,omitempty"`
}

// VSCodeTarget binds one ready VM to the caller-owned SSH identity used for it.
type VSCodeTarget struct {
	IdentityDir string
	Lease       domain.Lease
}

type resolvedVSCodeTarget struct {
	lease         domain.Lease
	sshConfig     string
	remote        string
	port          int
	systemSymbols string
}

// WriteVSCodeConfig builds on the host, copies runtime artifacts through pinned
// SSH, and connects host lldb-dap to guest debugserver through an SSH tunnel.
func WriteVSCodeConfig(workspace, identityDir string, lease domain.Lease) (VSCodeFiles, error) {
	return WriteVSCodeConfigs(workspace, []VSCodeTarget{{IdentityDir: identityDir, Lease: lease}})
}

// WriteVSCodeConfigs creates one workspace with a Run/Debug entry for each VM.
// The source workspace is read-only; all generated files remain lease-specific.
func WriteVSCodeConfigs(workspace string, targets []VSCodeTarget) (VSCodeFiles, error) {
	if len(targets) == 0 {
		return VSCodeFiles{}, fmt.Errorf("at least one Virfield VM is required")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return VSCodeFiles{}, err
	}
	st, err := os.Stat(workspace)
	if err != nil || !st.IsDir() {
		return VSCodeFiles{}, fmt.Errorf("workspace must be an existing directory")
	}
	project := filepath.Base(filepath.Clean(workspace))
	if !domain.ValidName(project) {
		return VSCodeFiles{}, fmt.Errorf("workspace directory name must be a valid Virfield name")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return VSCodeFiles{}, err
	}
	resolved := make([]resolvedVSCodeTarget, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target.Lease.ID] {
			return VSCodeFiles{}, fmt.Errorf("duplicate Virfield VM %s", target.Lease.ID)
		}
		seen[target.Lease.ID] = true
		identityDir, knownContent, configContent, err := sshConnectionFiles(target.IdentityDir, target.Lease)
		if err != nil {
			return VSCodeFiles{}, err
		}
		if err := requireExactPrivateFile(filepath.Join(identityDir, "known_hosts"), knownContent); err != nil {
			return VSCodeFiles{}, err
		}
		sshConfig := filepath.Join(identityDir, "config")
		if err := requireExactPrivateFile(sshConfig, configContent); err != nil {
			return VSCodeFiles{}, err
		}
		port := 43000 + int(crc32.ChecksumIEEE([]byte(target.Lease.ID))%1000)
		resolved = append(resolved, resolvedVSCodeTarget{
			lease: target.Lease, sshConfig: sshConfig,
			remote: "/Users/lume/workspace/" + project,
			port:   port, systemSymbols: deviceSupportForLease(home, target.Lease, port),
		})
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].lease.ID < resolved[j].lease.ID })
	vscodeDir := filepath.Join(workspace, ".vscode")
	duckWorkspace := filepath.Join(workspace, "DuckDuckGo-macOS.code-workspace")
	if st, statErr := os.Stat(duckWorkspace); statErr == nil && st.Mode().IsRegular() {
		return writeDuckDuckGoVSCodeConfig(workspace, vscodeDir, duckWorkspace, resolved)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return VSCodeFiles{}, statErr
	}
	if len(resolved) != 1 {
		return VSCodeFiles{}, fmt.Errorf("multiple VM selection requires DuckDuckGo-macOS.code-workspace")
	}
	target := resolved[0]
	sshConfig, remote, port, systemSymbols := target.sshConfig, target.remote, target.port, target.systemSymbols
	files := VSCodeFiles{
		Launch:   filepath.Join(vscodeDir, "launch.json"),
		Tasks:    filepath.Join(vscodeDir, "tasks.json"),
		Settings: filepath.Join(vscodeDir, "settings.json"),
		Adapter:  filepath.Join(vscodeDir, "virfield-remote-lldb-dap"),
		Prepare:  filepath.Join(vscodeDir, "virfield-remote-prepare"),
	}
	for _, path := range []string{files.Launch, files.Tasks, files.Settings, files.Adapter, files.Prepare} {
		if _, err := os.Lstat(path); err == nil {
			return VSCodeFiles{}, fmt.Errorf("refusing to overwrite %s", path)
		} else if !os.IsNotExist(err) {
			return VSCodeFiles{}, err
		}
	}
	if err := ensureLocalDirectory(vscodeDir); err != nil {
		return VSCodeFiles{}, err
	}

	adapter := "#!/bin/sh\nexec /usr/bin/xcrun lldb-dap \"$@\"\n"
	prepare := remoteDebugPrepareScript(sshConfig, target.lease, remote, port, "", systemSymbols)
	settings := map[string]any{"lldb-dap.executable-path": files.Adapter}
	localProgram := "${workspaceFolder}/.build/debug/${input:virfieldProduct}"
	remoteProgram := remote + "/.build/debug/${input:virfieldProduct}"
	launch := map[string]any{
		"version": "0.2.0",
		"configurations": []any{map[string]any{
			"name":           "Virfield: Debug SwiftPM product",
			"type":           "lldb-dap",
			"request":        "attach",
			"program":        localProgram,
			"cwd":            "${workspaceFolder}",
			"platformName":   "remote-macosx",
			"preLaunchTask":  "Virfield: Prepare debug",
			"initCommands":   lldbInitCommands(),
			"preRunCommands": lldbPreRunCommands("${workspaceFolder}/.build/debug", systemSymbols),
			"attachCommands": lldbAttachCommands(port),
		}},
		"inputs": []any{map[string]any{
			"id":          "virfieldProduct",
			"type":        "promptString",
			"description": "SwiftPM executable product",
			"default":     project,
		}},
	}
	hostBuild := "cd \"${workspaceFolder}\" && /usr/bin/xcrun swift build"
	hostTest := "cd \"${workspaceFolder}\" && /usr/bin/xcrun swift test"
	syncArtifact := "/usr/bin/ssh -T -F " + shellQuote(sshConfig) + " virfield /bin/mkdir -p " + shellQuote(remote+"/.build/debug") + " && /usr/bin/rsync -a --delete -e " + shellQuote("/usr/bin/ssh -T -F "+shellQuote(sshConfig)) + " \"${workspaceFolder}/.build/debug/\" " + shellQuote("virfield:"+remote+"/.build/debug/")
	tasks := map[string]any{
		"version": "2.0.0",
		"tasks": []any{
			map[string]any{
				"label":          "Virfield: Build",
				"type":           "shell",
				"command":        hostBuild,
				"problemMatcher": []string{},
				"group":          map[string]any{"kind": "build", "isDefault": true},
			},
			map[string]any{
				"label":          "Virfield: Sync artifacts",
				"type":           "shell",
				"command":        syncArtifact,
				"dependsOn":      "Virfield: Build",
				"problemMatcher": []string{},
			},
			map[string]any{
				"label":          "Virfield: Test",
				"type":           "shell",
				"command":        hostTest,
				"problemMatcher": []string{},
				"group":          map[string]any{"kind": "test", "isDefault": true},
			},
			map[string]any{
				"label": "Virfield: Prepare debug", "type": "process", "command": files.Prepare,
				"args": []string{remoteProgram}, "dependsOn": "Virfield: Sync artifacts", "problemMatcher": []string{},
			},
		},
	}

	created := []string{}
	rollback := func() {
		for _, path := range created {
			_ = os.Remove(path)
		}
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
		body any
	}{
		{files.Adapter, 0700, adapter},
		{files.Prepare, 0700, prepare},
		{files.Settings, 0600, settings},
		{files.Launch, 0600, launch},
		{files.Tasks, 0600, tasks},
	} {
		var data []byte
		if text, ok := item.body.(string); ok {
			data = []byte(text)
		} else {
			data, err = json.MarshalIndent(item.body, "", "  ")
			data = append(data, '\n')
		}
		if err != nil {
			rollback()
			return VSCodeFiles{}, err
		}
		if err := writeNewMode(item.path, data, item.mode); err != nil {
			rollback()
			return VSCodeFiles{}, err
		}
		created = append(created, item.path)
	}
	return files, nil
}

func writeDuckDuckGoVSCodeConfig(workspace, vscodeDir, sourceWorkspace string, targets []resolvedVSCodeTarget) (VSCodeFiles, error) {
	var identity strings.Builder
	for _, target := range targets {
		identity.WriteString(target.lease.ID)
		identity.WriteByte(0)
	}
	suffix := fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(identity.String())))
	files := VSCodeFiles{
		Workspace: filepath.Join(vscodeDir, "DuckDuckGo-macOS.virfield-remote-"+suffix+".code-workspace"),
		Adapter:   filepath.Join(vscodeDir, "virfield-remote-lldb-dap-"+suffix),
	}
	for _, target := range targets {
		files.Helpers = append(files.Helpers, filepath.Join(vscodeDir, "virfield-remote-prepare-"+target.lease.ID))
	}
	files.Prepare = files.Helpers[0]
	generated := append([]string{files.Workspace, files.Adapter}, files.Helpers...)
	for _, path := range generated {
		if _, err := os.Lstat(path); err == nil {
			return VSCodeFiles{}, fmt.Errorf("refusing to overwrite %s", path)
		} else if !os.IsNotExist(err) {
			return VSCodeFiles{}, err
		}
	}
	if err := ensureLocalDirectory(vscodeDir); err != nil {
		return VSCodeFiles{}, err
	}
	source, err := os.ReadFile(sourceWorkspace)
	if err != nil {
		return VSCodeFiles{}, err
	}
	var workspaceConfig map[string]any
	if err := json.Unmarshal(stripJSONComments(source), &workspaceConfig); err != nil {
		return VSCodeFiles{}, fmt.Errorf("parse %s: %w", sourceWorkspace, err)
	}
	workspaceConfig["folders"] = []any{map[string]any{"path": ".."}}
	settings, ok := workspaceConfig["settings"].(map[string]any)
	if !ok {
		settings = map[string]any{}
		workspaceConfig["settings"] = settings
	}
	settings["lldb-dap.executable-path"] = files.Adapter

	extensions, ok := workspaceConfig["extensions"].(map[string]any)
	if !ok {
		extensions = map[string]any{}
		workspaceConfig["extensions"] = extensions
	}
	recommendations, _ := extensions["recommendations"].([]any)
	foundDAP := false
	for _, recommendation := range recommendations {
		if recommendation == "llvm-vs-code-extensions.lldb-dap" {
			foundDAP = true
		}
	}
	if !foundDAP {
		recommendations = append(recommendations, "llvm-vs-code-extensions.lldb-dap")
	}
	extensions["recommendations"] = recommendations

	localPackageDir := filepath.Join(workspace, "macOS", "DuckDuckGo")
	localMacOSDir := filepath.Join(workspace, "macOS")
	localHostDir := filepath.Join(localMacOSDir, ".build", "vscode-host")
	localProgram := filepath.Join(localHostDir, "DuckDuckGo.app", "Contents", "MacOS", "DuckDuckGo")
	localProducts := filepath.Join(localMacOSDir, ".build", "DuckDuckGo", "out", "Products", "Debug")
	localTask := func(label, detail, command string) map[string]any {
		return map[string]any{
			"label":          label,
			"detail":         detail,
			"type":           "shell",
			"command":        command,
			"problemMatcher": []string{},
		}
	}
	buildHost := "cd " + shellQuote(localPackageDir) + " && ../scripts/vscode/make-host-app.sh --force && /usr/bin/codesign --force --deep --sign - --timestamp=none " + shellQuote(filepath.Join(localHostDir, "DuckDuckGo.app")) + " && /usr/bin/printf '%s\\n' - > " + shellQuote(filepath.Join(localHostDir, "identity"))
	buildLibrary := "cd " + shellQuote(localPackageDir) + " && /usr/bin/xcrun swift build --build-system swiftbuild --scratch-path ../.build/DuckDuckGo --product DuckDuckGoBrowserDynamic"
	signLibrary := "cd " + shellQuote(localPackageDir) + " && ../scripts/vscode/sign-dylib.sh"
	testPackage := "cd " + shellQuote(localPackageDir) + " && /usr/bin/xcrun swift test --build-system swiftbuild --scratch-path ../.build/DuckDuckGo"
	hostTask := localTask("Virfield: Build host app", "Build and ad-hoc sign the launcher on this Mac", buildHost)
	libraryTask := localTask("Virfield: Build browser library", "Build DuckDuckGoBrowserDynamic on this Mac with swiftbuild", buildLibrary)
	signTask := localTask("Virfield: Sign browser library", "Ad-hoc sign the host-built dynamic products", signLibrary)
	testTask := localTask("Virfield: Test", "Run DuckDuckGo SwiftPM tests on this Mac", testPackage)
	testTask["group"] = map[string]any{"kind": "test", "isDefault": true}
	tasks := []any{
		hostTask,
		libraryTask,
		signTask,
		map[string]any{
			"label":          "Virfield: Build DuckDuckGo",
			"dependsOrder":   "sequence",
			"dependsOn":      []string{"Virfield: Build host app", "Virfield: Build browser library", "Virfield: Sign browser library"},
			"group":          map[string]any{"kind": "build", "isDefault": true},
			"problemMatcher": []string{},
		},
		testTask,
		map[string]any{
			"label": "Virfield: Open console", "type": "process", "command": "/usr/bin/open",
			"args": []string{"http://127.0.0.1:7780"}, "problemMatcher": []string{},
		},
	}
	launches := []any{}
	prepares := make([]string, 0, len(targets))
	for i, target := range targets {
		vm := target.lease.VMName + " [" + target.lease.ID + "]"
		remoteHostDir := target.remote + "/macOS/.build/vscode-host"
		remoteProducts := target.remote + "/macOS/.build/DuckDuckGo/out/Products/Debug"
		remoteProgram := remoteHostDir + "/DuckDuckGo.app/Contents/MacOS/DuckDuckGo"
		sshTransport := "/usr/bin/ssh -T -F " + shellQuote(target.sshConfig)
		syncArtifacts := "/usr/bin/ssh -T -F " + shellQuote(target.sshConfig) + " virfield /bin/mkdir -p " + shellQuote(remoteHostDir) + " " + shellQuote(remoteProducts) + " && /usr/bin/rsync -a --delete -e " + shellQuote(sshTransport) + " " + shellQuote(filepath.Join(localHostDir, "DuckDuckGo.app")) + " " + shellQuote("virfield:"+remoteHostDir+"/") + " && /usr/bin/rsync -a --delete -e " + shellQuote(sshTransport) + " " + shellQuote(localProducts+"/") + " " + shellQuote("virfield:"+remoteProducts+"/")
		syncLabel := "Virfield: Sync artifacts — " + vm
		prepareLabel := "Virfield: Prepare DuckDuckGo debug — " + vm
		tasks = append(tasks,
			map[string]any{
				"label": syncLabel, "type": "shell", "command": syncArtifacts,
				"dependsOn": "Virfield: Build DuckDuckGo", "problemMatcher": []string{},
			},
			map[string]any{
				"label": prepareLabel, "type": "process", "command": files.Helpers[i],
				"dependsOn": syncLabel, "problemMatcher": []string{},
			},
			map[string]any{
				"label": "Virfield: Open VM terminal — " + vm, "type": "process", "command": "/usr/bin/ssh",
				"args": []string{"-F", target.sshConfig, "virfield"}, "problemMatcher": []string{},
				"presentation": map[string]any{"reveal": "always", "focus": true, "panel": "dedicated"},
			},
		)
		launches = append(launches, map[string]any{
			"name":           "DuckDuckGo — Virfield VM: " + vm,
			"type":           "lldb-dap",
			"request":        "attach",
			"program":        localProgram,
			"cwd":            localPackageDir,
			"platformName":   "remote-macosx",
			"preLaunchTask":  prepareLabel,
			"initCommands":   lldbInitCommands(),
			"preRunCommands": lldbPreRunCommands(localProducts, target.systemSymbols),
			"attachCommands": lldbAttachCommands(target.port),
			"presentation": map[string]any{
				"group": "Virfield", "order": i + 1, "hidden": false,
			},
		})
		prepares = append(prepares, remoteDebugPrepareScript(target.sshConfig, target.lease, target.remote, target.port, remoteProgram, target.systemSymbols))
	}
	workspaceConfig["tasks"] = map[string]any{
		"version": "2.0.0",
		"tasks":   tasks,
	}
	workspaceConfig["launch"] = map[string]any{
		"version":        "0.2.0",
		"configurations": launches,
	}

	adapter := "#!/bin/sh\nexec /usr/bin/xcrun lldb-dap \"$@\"\n"
	workspaceData, err := json.MarshalIndent(workspaceConfig, "", "  ")
	if err != nil {
		return VSCodeFiles{}, err
	}
	workspaceData = append(workspaceData, '\n')
	if err := writeNewMode(files.Adapter, []byte(adapter), 0700); err != nil {
		return VSCodeFiles{}, err
	}
	created := []string{files.Adapter}
	for i, helper := range files.Helpers {
		if err := writeNewMode(helper, []byte(prepares[i]), 0700); err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
			return VSCodeFiles{}, err
		}
		created = append(created, helper)
	}
	if err := writeNewMode(files.Workspace, workspaceData, 0600); err != nil {
		for _, path := range created {
			_ = os.Remove(path)
		}
		return VSCodeFiles{}, err
	}
	return files, nil
}

func lldbInitCommands() []string {
	return []string{
		"settings set symbols.load-on-demand true",
		"settings set symbols.shared-cache-binary-loading inferior-shared-cache-only",
		"settings set target.memory-module-load-level minimal",
	}
}

func lldbPreRunCommands(localProducts, systemSymbols string) []string {
	return []string{
		"settings set target.exec-search-paths " + lldbQuote(localProducts) + " " + lldbQuote(filepath.Join(systemSymbols, "usr", "lib")),
	}
}

func lldbAttachCommands(port int) []string {
	return []string{
		"gdb-remote 127.0.0.1:" + strconv.Itoa(port),
	}
}

func remoteDebugPrepareScript(sshConfig string, lease domain.Lease, remote string, port int, fixedProgram, systemSymbols string) string {
	return `#!/bin/zsh
set -euo pipefail
ssh_config=` + shellQuote(sshConfig) + `
vm_ip=` + shellQuote(lease.IP) + `
port=` + strconv.Itoa(port) + `
socket=` + shellQuote(filepath.Join("/private/tmp", "virfield-vscode-"+strconv.Itoa(port)+".sock")) + `
dyld_mount=/private/tmp/virfield-dyld-` + strconv.Itoa(port) + `
system_symbols=` + shellQuote(systemSymbols) + `
remote_root=` + shellQuote(remote) + `
remote_program=` + shellQuote(fixedProgram) + `
if [[ -z "$remote_program" ]]; then
  remote_program="${1:?remote SwiftPM executable is required}"
  [[ "$remote_program" == "$remote_root/.build/debug/"* ]] || { echo 'invalid remote executable' >&2; exit 64; }
fi
if [[ ! -f "$system_symbols/usr/lib/libobjc.A.dylib" ]]; then
  /bin/mkdir -p "$dyld_mount"
  if ! /sbin/mount | /usr/bin/grep -Fq " on $dyld_mount ("; then
    dyld_share=$(/usr/bin/ssh -T -F "$ssh_config" virfield /bin/bash -c 'if test -d /System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld; then printf %s /System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld; else printf %s /System/Library/dyld; fi')
    /sbin/mount_nfs -o ro,nolocks "$vm_ip:$dyld_share" "$dyld_mount"
  fi
  build=$(/usr/bin/ssh -T -F "$ssh_config" virfield /usr/bin/sw_vers -buildVersion)
  echo "Preparing system symbols for macOS $build from the NFS dyld cache (one time)..."
  symbol_parent=$(/usr/bin/dirname "$system_symbols")
  /bin/mkdir -p "$symbol_parent"
  source_file="$symbol_parent/dsc-extractor.c"
  extractor="$symbol_parent/dsc-extractor"
  /bin/cat >"$source_file" <<'EXTRACTOR'
#include <dlfcn.h>
#include <stdio.h>
typedef int (*extract_fn)(const char *, const char *, void (^)(unsigned, unsigned));
int main(int argc, char **argv) {
  if (argc != 4) return 64;
  void *handle = dlopen(argv[1], RTLD_NOW);
  if (!handle) { fprintf(stderr, "dlopen: %s\n", dlerror()); return 65; }
  extract_fn extract = (extract_fn)dlsym(handle, "dyld_shared_cache_extract_dylibs_progress");
  if (!extract) { fprintf(stderr, "dlsym: %s\n", dlerror()); return 66; }
  return extract(argv[2], argv[3], ^(unsigned current, unsigned total) {
    if (current == total || current % 250 == 0) fprintf(stderr, "%u/%u\n", current, total);
  });
}
EXTRACTOR
  /usr/bin/xcrun clang -fblocks "$source_file" -o "$extractor" -ldl
  developer=$(/usr/bin/xcode-select -p)
  bundle="$developer/Platforms/iPhoneOS.platform/usr/lib/dsc_extractor.bundle"
  test -d "$bundle"
  staging=$(/usr/bin/mktemp -d "$symbol_parent/.Symbols-$build.XXXXXX")
  "$extractor" "$bundle" "$dyld_mount/dyld_shared_cache_arm64e" "$staging"
  if [[ -e "$system_symbols" ]]; then
    /bin/mv "$system_symbols" "$system_symbols.invalid.$(/bin/date +%s)"
  fi
  /bin/mv "$staging" "$system_symbols"
fi
/usr/bin/ssh -T -F "$ssh_config" virfield /bin/bash -s -- "$remote_program" "$port" <<'REMOTE'
set -eu
program="$1"
port="$2"
test -x "$program"
/usr/bin/pkill -x debugserver 2>/dev/null || true
/usr/bin/pkill -x "$(/usr/bin/basename "$program")" 2>/dev/null || true
debugserver=/Applications/Xcode.app/Contents/SharedFrameworks/LLDB.framework/Versions/A/Resources/debugserver
test -x "$debugserver"
/usr/bin/nohup "$debugserver" "127.0.0.1:$port" "$program" >"/tmp/virfield-debugserver-$port.log" 2>&1 </dev/null &
for attempt in {1..50}; do
  /usr/bin/pgrep -x debugserver >/dev/null && exit 0
  /bin/sleep 0.1
done
exit 70
REMOTE
/usr/bin/ssh -S "$socket" -O exit -F "$ssh_config" virfield >/dev/null 2>&1 || true
/bin/rm -f -- "$socket"
/usr/bin/ssh -fNT -M -S "$socket" -F "$ssh_config" -L "127.0.0.1:$port:127.0.0.1:$port" virfield
`
}

func lldbQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func deviceSupportForLease(home string, lease domain.Lease, port int) string {
	fallback := filepath.Join(home, "Library", "Caches", "Virfield", "dyld", "lease-"+strconv.Itoa(port))
	if lease.Source == nil || lease.Source.Image == nil {
		return fallback
	}
	image := lease.Source.Image
	if !domain.ValidName(image.Build) {
		return fallback
	}
	root := filepath.Join(home, "Library", "Developer", "Xcode", "macOS DeviceSupport")
	if domain.ValidVersion(image.MacOS) {
		return filepath.Join(root, image.MacOS+" ("+image.Build+")", "Symbols")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fallback
	}
	suffix := " (" + image.Build + ")"
	match := ""
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		candidate := filepath.Join(root, entry.Name(), "Symbols", "usr", "lib", "libobjc.A.dylib")
		if st, statErr := os.Lstat(candidate); statErr == nil && st.Mode().IsRegular() {
			if match != "" {
				return fallback
			}
			match = filepath.Join(root, entry.Name(), "Symbols")
		}
	}
	if match != "" {
		return match
	}
	return fallback
}

func stripJSONComments(source []byte) []byte {
	out := make([]byte, 0, len(source))
	inString := false
	escaped := false
	lineComment := false
	blockComment := false
	for i := 0; i < len(source); i++ {
		b := source[i]
		if lineComment {
			if b == '\n' {
				lineComment = false
				out = append(out, b)
			}
			continue
		}
		if blockComment {
			if b == '*' && i+1 < len(source) && source[i+1] == '/' {
				blockComment = false
				i++
			} else if b == '\n' {
				out = append(out, b)
			}
			continue
		}
		if inString {
			out = append(out, b)
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}
		if b == '/' && i+1 < len(source) && source[i+1] == '/' {
			lineComment = true
			i++
			continue
		}
		if b == '/' && i+1 < len(source) && source[i+1] == '*' {
			blockComment = true
			i++
			continue
		}
		out = append(out, b)
	}
	return out
}

func requireExactPrivateFile(path, expected string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 32*1024 {
		return fmt.Errorf("%s must be an owner-only regular file", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(b) != expected {
		return fmt.Errorf("%s does not match the ready lease", path)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func ensureLocalDirectory(path string) error {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return os.Mkdir(path, 0700)
	}
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s must be a directory, not a symlink or file", path)
	}
	return nil
}

func writeNewMode(path string, content []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(path)
	}
	return closeErr
}
