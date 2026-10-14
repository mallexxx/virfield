package baremetal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testVMImage = "docker.io/dockurr/macos@sha256:08d1bcaac74ad44d548b7ce683b3dded083512d2a0d26416f9a620b763f8ea3d"

// fakePodman records its argv; inspect reports running only for containers
// listed in $HOME/running, which run -d adds to and stop/kill/rm remove from.
func fakePodman(t *testing.T) (bin, log string) {
	t.Helper()
	bin = t.TempDir()
	log = filepath.Join(bin, "podman.log")
	script := `#!/bin/sh
echo "$@" >> ` + log + `
drop() { grep -vx "$1" "$HOME/running" > "$HOME/running.t" 2>/dev/null; mv "$HOME/running.t" "$HOME/running" 2>/dev/null; true; }
drop_container() { grep -vx "$1" "$HOME/containers" > "$HOME/containers.t" 2>/dev/null; mv "$HOME/containers.t" "$HOME/containers" 2>/dev/null; true; }
case "$1" in
  container) grep -qx "$3" "$HOME/containers" 2>/dev/null && exit 0; exit 1 ;;
  inspect) grep -qx "$4" "$HOME/running" 2>/dev/null && echo true && exit 0; grep -qx "$4" "$HOME/containers" 2>/dev/null && echo false && exit 0; exit 125 ;;
  run) [ "$2" = -d ] && [ "$3" = --name ] && { echo "$4" >> "$HOME/running"; echo "$4" >> "$HOME/containers"; } ;;
  stop) drop "$4" ;;
  kill) drop "$2" ;;
  rm) [ ! -f "$HOME/fail-rm" ] || exit 125; drop "$3"; drop_container "$3" ;;
esac
case "$*" in *" convert "*) eval "out=\${$#}"; echo disk > "$out" ;; esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	// The guest SSH banner check reads the forwarded port; guests listed in
	// $HOME/sshd answer with a banner.
	banner := `#!/bin/sh
a="$*"
case "$a" in *"/dev/tcp/127.0.0.1/"*) p=${a##*/dev/tcp/127.0.0.1/}; p=${p%% *}; grep -qx "$p" "$HOME/sshd" 2>/dev/null && echo SSH-2.0-OpenSSH_9.8 ;; esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "timeout"), []byte(banner), 0755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func runVMScript(t *testing.T, home, bin, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{"-c", script, "sh"}, args...)...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin:/usr/sbin:/sbin"}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func goldensDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		for _, f := range []string{n + ".qcow2", n + ".opencore.plist"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte(n), 0444); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

func TestVMCloneAllocatesPortsWithinMax(t *testing.T) {
	home, goldens := t.TempDir(), goldensDir(t, "sonoma-secure")
	bin, log := fakePodman(t)
	for _, vm := range []string{"vf-aaaaaaaaaaaaaaaa", "vf-bbbbbbbbbbbbbbbb"} {
		if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", vm, "sonoma-secure", goldens, testVMImage, "22100", "2"); err != nil {
			t.Fatal(out, err)
		}
	}
	leases := filepath.Join(home, "virfield", "leases")
	if read(t, filepath.Join(leases, "vf-aaaaaaaaaaaaaaaa", "port")) != "22100" || read(t, filepath.Join(leases, "vf-bbbbbbbbbbbbbbbb", "port")) != "22101" {
		t.Fatal("ports not allocated in order")
	}
	if read(t, filepath.Join(leases, "vf-aaaaaaaaaaaaaaaa", "golden")) != "sonoma-secure" || read(t, filepath.Join(leases, "vf-aaaaaaaaaaaaaaaa", "kind")) != "vm" {
		t.Fatal("clone not recorded")
	}
	if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", "vf-cccccccccccccccc", "sonoma-secure", goldens, testVMImage, "22100", "2"); err == nil {
		t.Fatal("clone admitted beyond max", out)
	}
	if _, err := os.Stat(filepath.Join(leases, "vf-cccccccccccccccc")); !os.IsNotExist(err) {
		t.Fatal("refused clone left a folder")
	}
	want := "run --rm --entrypoint qemu-img -v " + goldens + ":" + goldens + ":ro,z -v " + filepath.Join(leases, "vf-aaaaaaaaaaaaaaaa", "storage") + ":/out:Z " + testVMImage + " create -q -f qcow2 -b " + goldens + "/sonoma-secure.qcow2 -F qcow2 /out/data.qcow2"
	if !strings.Contains(read(t, log), want) {
		t.Fatal(read(t, log))
	}
	if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", "vf-dddddddddddddddd", "missing", goldens, testVMImage, "22100", "4"); err == nil {
		t.Fatal("cloned a missing golden", out)
	}
}

func TestVMStartPublishesItsPortAndHoldsTheNode(t *testing.T) {
	home, goldens := t.TempDir(), goldensDir(t, "sonoma-secure")
	bin, log := fakePodman(t)
	hold := filepath.Join(t.TempDir(), "rsync-active")
	vm := "vf-aaaaaaaaaaaaaaaa"
	if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", vm, "sonoma-secure", goldens, testVMImage, "22100", "2"); err != nil {
		t.Fatal(out, err)
	}
	for range 2 {
		if out, err := runVMScript(t, home, bin, vmStartScript, "leases", vm, goldens, testVMImage, "0.0.0.0", "4", "8", "100G", hold, "127.0.0.1:23100"); err != nil {
			t.Fatal(out, err)
		}
	}
	calls := read(t, log)
	if strings.Count(calls, "run -d --name virfield-"+vm) != 1 {
		t.Fatal("start is not idempotent:", calls)
	}
	for _, want := range []string{"-p 0.0.0.0:22100:22", "-p 127.0.0.1:23100:5900", "-e USER_PORTS=22,5900", "-e VNC_PORT=5950", "--device /dev/kvm", "--device /dev/net/tun", "-e RAM_SIZE=8G", "-e CPU_CORES=4", "-e DISK_SIZE=100G", "-v " + goldens + ":" + goldens + ":ro,z", "-v " + goldens + "/sonoma-secure.opencore.plist:/custom.plist:ro,z"} {
		if !strings.Contains(calls, want) {
			t.Fatal("missing", want, calls)
		}
	}
	if _, err := os.Stat(hold); err != nil {
		t.Fatal("node not held awake")
	}
	if out, err := runVMScript(t, home, bin, vmStopScript, "leases", vm, hold); err != nil {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(hold); !os.IsNotExist(err) {
		t.Fatal("hold not released")
	}
	if !strings.Contains(read(t, log), "rm -f virfield-"+vm) || read(t, filepath.Join(home, "virfield", "leases", vm, "state")) != "stopped" {
		t.Fatal("stop did not remove the container")
	}
	if _, err := os.Stat(filepath.Join(home, "virfield", "leases", vm, "port")); err != nil {
		t.Fatal("stop removed the clone")
	}
	if out, err := runVMScript(t, home, bin, vmDeleteScript, "leases", vm, hold); err != nil {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(home, "virfield", "leases", vm)); !os.IsNotExist(err) {
		t.Fatal("delete kept the clone")
	}
}

func TestVMProbeReportsGoldensAndVMs(t *testing.T) {
	home, goldens := t.TempDir(), goldensDir(t, "sonoma-secure", "sequoia-secure")
	bin, _ := fakePodman(t)
	for i, vm := range []string{"vf-aaaaaaaaaaaaaaaa", "vf-bbbbbbbbbbbbbbbb"} {
		if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", vm, "sonoma-secure", goldens, testVMImage, "22100", "2"); err != nil {
			t.Fatal(i, out, err)
		}
	}
	if out, err := runVMScript(t, home, bin, vmStartScript, "leases", "vf-aaaaaaaaaaaaaaaa", goldens, testVMImage, "0.0.0.0", "4", "8", "100G", ""); err != nil {
		t.Fatal(out, err)
	}
	if out, err := runVMScript(t, home, bin, vmCloneScript, "images", "htpc-sonoma", "sonoma-secure", goldens, testVMImage, "22102", "2"); err != nil {
		t.Fatal(out, err)
	}
	out, err := runVMScript(t, home, bin, vmProbeLines, goldens)
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out, "vm=leases vf-aaaaaaaaaaaaaaaa running 22100 sonoma-secure 0\n") {
		t.Fatal("guest SSH reported before its banner:", out)
	}
	if err := os.WriteFile(filepath.Join(home, "sshd"), []byte("22100\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, _ = runVMScript(t, home, bin, vmProbeLines, goldens)
	for _, want := range []string{"golden=sonoma-secure\n", "golden=sequoia-secure\n", "vm=leases vf-aaaaaaaaaaaaaaaa running 22100 sonoma-secure 1\n", "vm=leases vf-bbbbbbbbbbbbbbbb stopped 22101 sonoma-secure 0\n", "vm=images htpc-sonoma stopped 22102 sonoma-secure 0\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}

func TestVMCommitWritesAReadOnlyGolden(t *testing.T) {
	home, goldens := t.TempDir(), goldensDir(t, "sonoma-bootstrap")
	if err := os.Chmod(goldens, 0755); err != nil {
		t.Fatal(err)
	}
	bin, log := fakePodman(t)
	if out, err := runVMScript(t, home, bin, vmCloneScript, "images", "htpc-sonoma", "sonoma-bootstrap", goldens, testVMImage, "22102", "2"); err != nil {
		t.Fatal(out, err)
	}
	// The fake qemu-img create writes nothing either.
	if err := os.WriteFile(filepath.Join(home, "virfield", "images", "htpc-sonoma", "storage", "data.qcow2"), []byte("overlay"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := runVMScript(t, home, bin, vmCommitScript, "images", "htpc-sonoma", goldens, testVMImage); err != nil {
		t.Fatal(out, err)
	}
	info, err := os.Stat(filepath.Join(goldens, "htpc-sonoma.qcow2"))
	if err != nil || info.Mode().Perm() != 0444 || read(t, filepath.Join(goldens, "htpc-sonoma.opencore.plist")) != "sonoma-bootstrap" {
		t.Fatal("secured golden not written read-only with its OpenCore config", err)
	}
	if !strings.Contains(read(t, log), "convert -O qcow2 /src/data.qcow2 "+goldens+"/.htpc-sonoma.tmp") {
		t.Fatal(read(t, log))
	}
	if _, err := os.Stat(filepath.Join(home, "virfield", "images", "htpc-sonoma")); !os.IsNotExist(err) {
		t.Fatal("image clone kept after commit")
	}
	if out, err := runVMScript(t, home, bin, vmCommitScript, "images", "htpc-sonoma", goldens, testVMImage); err == nil {
		t.Fatal("commit overwrote a golden", out)
	}
}

func TestHostScriptsRefuseVMFolders(t *testing.T) {
	home, goldens := t.TempDir(), goldensDir(t, "sonoma-secure")
	bin, _ := fakePodman(t)
	vm := "vf-aaaaaaaaaaaaaaaa"
	if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", vm, "sonoma-secure", goldens, testVMImage, "22100", "2"); err != nil {
		t.Fatal(out, err)
	}
	for name, script := range map[string]string{"start": startScript, "stop": stopScript, "delete": deleteScript} {
		if out, err := runVMScript(t, home, bin, script, vm, ""); err == nil {
			t.Fatal(name, "accepted a node VM folder", out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "virfield", "leases", vm, "port")); err != nil {
		t.Fatal("host script damaged a VM folder")
	}
}

func TestNodeSleepHoldLivesUntilLastVMAndPreservesExternalFile(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "external"}[external], func(t *testing.T) {
			home, goldens := t.TempDir(), goldensDir(t, "secure")
			bin, _ := fakePodman(t)
			hold := filepath.Join(home, "hold")
			if external {
				if err := os.WriteFile(hold, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"vf-one", "vf-two"} {
				if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", name, "secure", goldens, testVMImage, "22100", "2"); err != nil {
					t.Fatal(out, err)
				}
				if out, err := runVMScript(t, home, bin, vmStartScript, "leases", name, goldens, testVMImage, "0.0.0.0", "4", "8", "100G", hold); err != nil {
					t.Fatal(out, err)
				}
			}
			if out, err := runVMScript(t, home, bin, vmDeleteScript, "leases", "vf-one", hold); err != nil {
				t.Fatal(out, err)
			}
			if _, err := os.Stat(hold); err != nil {
				t.Fatal("remaining VM lost sleep hold", err)
			}
			if out, err := runVMScript(t, home, bin, vmDeleteScript, "leases", "vf-two", hold); err != nil {
				t.Fatal(out, err)
			}
			_, err := os.Stat(hold)
			if external && err != nil || !external && !os.IsNotExist(err) {
				t.Fatal("wrong final ownership", err)
			}
		})
	}
}
func TestNodeDeleteKeepsOverlayAndHoldWhenContainerRemovalFails(t *testing.T) {
	home, goldens := t.TempDir(), goldensDir(t, "secure")
	bin, _ := fakePodman(t)
	hold := filepath.Join(home, "hold")
	if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", "vf-one", "secure", goldens, testVMImage, "22100", "2"); err != nil {
		t.Fatal(out, err)
	}
	if out, err := runVMScript(t, home, bin, vmStartScript, "leases", "vf-one", goldens, testVMImage, "0.0.0.0", "4", "8", "100G", hold); err != nil {
		t.Fatal(out, err)
	}
	if err := os.WriteFile(filepath.Join(home, "fail-rm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := runVMScript(t, home, bin, vmDeleteScript, "leases", "vf-one", hold); err == nil {
		t.Fatal("failed removal reported cleanup", out)
	}
	if _, err := os.Stat(filepath.Join(home, "virfield/leases/vf-one")); err != nil {
		t.Fatal("overlay was removed", err)
	}
	if _, err := os.Stat(hold); err != nil {
		t.Fatal("sleep hold was removed", err)
	}
}
func TestNodeImageDeleteProtectsBackingGoldens(t *testing.T) {
	home, goldens := t.TempDir(), goldensDir(t, "secure")
	bin, _ := fakePodman(t)
	if out, err := runVMScript(t, home, bin, vmCloneScript, "leases", "vf-one", "secure", goldens, testVMImage, "22100", "2"); err != nil {
		t.Fatal(out, err)
	}
	if out, err := runVMScript(t, home, bin, vmImageDeleteScript, "images", "secure", goldens, ""); err == nil {
		t.Fatal("deleted backing golden", out)
	}
	if _, err := os.Stat(filepath.Join(goldens, "secure.qcow2")); err != nil {
		t.Fatal(err)
	}
	if out, err := runVMScript(t, home, bin, vmDeleteScript, "leases", "vf-one", ""); err != nil {
		t.Fatal(out, err)
	}
	if out, err := runVMScript(t, home, bin, vmImageDeleteScript, "images", "secure", goldens, ""); err != nil {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(goldens, "secure.qcow2")); !os.IsNotExist(err) {
		t.Fatal("golden retained", err)
	}
}
