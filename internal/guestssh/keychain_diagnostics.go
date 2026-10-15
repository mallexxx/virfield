package guestssh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReportKeychainFailure retains bounded private diagnostics while the existing
// provisioning SSH connection can still run commands. Never read identity or
// keychain contents and never attach this output to an API error or event.
func (g *Client) ReportKeychainFailure(ctx context.Context, stage, output string) {
	if g.diagnosticDir == "" {
		return
	}
	read, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	logs, err := g.RunReader(read, 15*time.Second, keychainDiagnostics, strings.NewReader(""))
	if err != nil {
		logs += "\nDiagnostic collection did not complete.\n"
	}
	_ = writeKeychainDiagnostic(g.diagnosticDir, stage, output, logs)
}

const keychainDiagnostics = `set +x
/bin/date -u
/usr/bin/stat -f '%Su' /dev/console
/usr/sbin/sysctl -n kern.bootsessionuuid
for name in phase agent.log display-result login-result result startup desktop-result; do
  file="$HOME/.virfield-keychain/$name"
  if [ -f "$file" ] && [ ! -L "$file" ]; then
    printf '\n%s\n' "$name"
    /usr/bin/stat -f '%m %z' "$file"
    /usr/bin/head -c 8192 "$file"
  fi
done
# Read only our UID's fixed native probe, at most two one-second samples.
# Never dump environment, shell arguments, identity or keychain contents.
for pid in $(/usr/bin/pgrep -U "$(/usr/bin/id -u)" -f 'osascript -l JavaScript .*\.virfield-keychain/probe.js' | /usr/bin/head -n 2); do
  case "$pid" in *[!0-9]*|'') continue ;; esac
  sample="$(/usr/bin/mktemp "$HOME/.virfield-keychain/.native-stack.XXXXXX")" || continue
  /usr/bin/sample "$pid" 1 10 -file "$sample" >/dev/null 2>&1
  printf '\nnative-stack %s\n' "$pid"
  /usr/bin/head -c 16384 "$sample"
  /bin/rm -f "$sample"
done`

func writeKeychainDiagnostic(directory, stage, output, logs string) error {
	f, err := os.CreateTemp(directory, ".keychain-log-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.WriteString("Keychain failure stage: " + stage + "\n" + output + "\n" + logs); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(directory, "keychain.log"))
}
