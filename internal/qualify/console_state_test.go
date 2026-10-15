package qualify

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/mallexxx/virfield/internal/guestscripts"
)

func TestNativeConsoleStateHandlesQuartzAbsentUnlockedFlag(t *testing.T) {
	for name, source := range map[string]string{"preflight": DesktopProbe, "Aqua": guestscripts.Keychain} {
		t.Run(name, func(t *testing.T) {
			begin := strings.Index(source, "// ai.virfield.console-state begin")
			end := strings.Index(source, "// ai.virfield.console-state end")
			if begin < 0 || end < begin {
				t.Fatal("Missing actual native console predicate")
			}
			script := source[begin:end] + `
function run() {
 var live = {kCGSSessionOnConsoleKey:true,kCGSessionLoginDoneKey:true};
 var cases = [null, {}, live,
  {kCGSSessionOnConsoleKey:true,kCGSessionLoginDoneKey:true,CGSSessionScreenIsLocked:false},
  {kCGSSessionOnConsoleKey:true,kCGSessionLoginDoneKey:true,CGSSessionScreenIsLocked:true},
  {kCGSSessionOnConsoleKey:false,kCGSessionLoginDoneKey:true},
  {kCGSSessionOnConsoleKey:true,kCGSessionLoginDoneKey:false},
  {kCGSSessionOnConsoleKey:true,kCGSessionLoginDoneKey:true,CGSSessionScreenIsLocked:0}];
 return JSON.stringify(cases.map(virfieldConsoleState));
}`
			var cmd *exec.Cmd
			if runtime.GOOS == "darwin" {
				cmd = exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", script)
			} else {
				node, err := exec.LookPath("node")
				if err != nil {
					t.Skip("Native macOS predicate requires JXA or Node")
				}
				cmd = exec.Command(node, "-e", script+"\nprocess.stdout.write(run());")
			}
			out, err := cmd.CombinedOutput()
			var got []struct {
				Ready  bool  `json:"ready"`
				Locked *bool `json:"screen_locked"`
			}
			if err != nil || json.Unmarshal(out, &got) != nil || len(got) != 8 {
				t.Fatal("Native predicate did not run", string(out), err)
			}
			for i, want := range []bool{false, false, true, true, false, false, false, false} {
				if got[i].Ready != want {
					t.Fatal("Wrong console state", i, got[i])
				}
			}
			if got[2].Locked == nil || *got[2].Locked || got[4].Locked == nil || !*got[4].Locked || got[7].Locked != nil {
				t.Fatal("Locked/unknown semantics lost", got)
			}
		})
	}
}
