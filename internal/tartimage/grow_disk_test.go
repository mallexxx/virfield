package tartimage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGrowDiskUsesDataCapacityAndWaitsWithoutRepeatingResize(t *testing.T) {
	script, err := recipe.ReadFile("recipe/scripts/grow-disk.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode    string
		success bool
		resize  bool
	}{
		{"already", true, false},
		{"delayed", true, true},
		{"full", false, true},
		{"invalid", false, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			commands := map[string]string{
				"df": `#!/bin/bash
n=0
[[ ! -f "$VF_TEST_ROOT/checks" ]] || n=$(cat "$VF_TEST_ROOT/checks")
printf '%s' "$((n+1))" > "$VF_TEST_ROOT/checks"
free=14000000
for path; do :; done
if [[ "$path" == /System/Volumes/Data ]]; then
  case "$VF_TEST_SPACE_MODE" in
    already) free=80000000 ;;
    delayed) if [[ -f "$VF_TEST_ROOT/resized" && "$n" -ge 3 ]]; then free=80000000; fi ;;
    invalid) free=unknown ;;
  esac
fi
printf 'Filesystem 1024-blocks Used Available Capacity Mounted\ndiskData 120000000 30000000 %s 25%% %s\n' "$free" "$path"
`,
				"diskutil": `#!/bin/sh
printf '%s\n' "$*" >> "$VF_TEST_ROOT/mutations"
case "$1 $2" in
  'apfs resizeContainer') touch "$VF_TEST_ROOT/resized" ;;
  'info -plist') echo plist ;;
esac
`,
				"plutil": "#!/bin/sh\ncat >/dev/null\necho disk0s2\n",
				"sudo":   "#!/bin/sh\n[ \"$1\" != -n ] || shift\nexec \"$@\"\n",
				"sleep":  "#!/bin/sh\nexit 0\n",
			}
			for name, body := range commands {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("/bin/bash", "-c", string(script))
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "VF_TEST_ROOT="+dir, "VF_TEST_SPACE_MODE="+tc.mode)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("success=%v: %s %v", tc.success, out, err)
			}
			mutations, _ := os.ReadFile(filepath.Join(dir, "mutations"))
			want := 0
			if tc.resize {
				want = 1
			}
			if strings.Count(string(mutations), "apfs resizeContainer disk0s2 0") != want {
				t.Fatal("resize was skipped or replayed", string(mutations))
			}
			checks, _ := os.ReadFile(filepath.Join(dir, "checks"))
			count, _ := strconv.Atoi(string(checks))
			if count < 1 || count > 40 {
				t.Fatal("capacity checks must be bounded", count)
			}
		})
	}
}
