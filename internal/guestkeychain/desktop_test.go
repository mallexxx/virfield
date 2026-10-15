package guestkeychain

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/guestscripts"
)

func TestAquaWorkerDesktopProbeUsesRealProtocolAndBoundsMissingGreeting(t *testing.T) {
	start := strings.Index(guestscripts.Keychain, "# ai.virfield.desktop-protocol begin")
	end := strings.Index(guestscripts.Keychain, "# ai.virfield.desktop-protocol end")
	if start < 0 || end <= start {
		t.Fatal("Missing automatic desktop protocol probe")
	}
	for _, test := range []struct {
		name, scope, greeting string
		ready, marker         bool
	}{
		{"fragmented worker greeting", "lease-fixture", "RFB 003.889\n", true, true},
		{"wrong service", "lease-fixture", "not RFB 123\n", false, false},
		{"silent service", "lease-fixture", "", false, false},
		{"image does not require worker desktop", "image-fixture", "", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan struct{})
			go func() {
				defer close(serverDone)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				for _, b := range []byte(test.greeting) {
					time.Sleep(2 * time.Millisecond)
					_, _ = conn.Write([]byte{b})
				}
				_, _ = io.Copy(io.Discard, conn)
			}()
			_, port, _ := net.SplitHostPort(listener.Addr().String())
			block := strings.ReplaceAll(guestscripts.Keychain[start:end], "/127.0.0.1/5900", "/127.0.0.1/"+port)
			block = strings.ReplaceAll(block, "SECONDS+90", "SECONDS+1")
			directory := t.TempDir()
			harness := "set -eu\ndir=\"$VIRFIELD_TEST_DIR\"\nscope=\"$VIRFIELD_TEST_SCOPE\"\nboot=fixture-boot\nphase() { :; }\n"
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/bash", "-c", harness+block+"\nprintf desktop-ready")
			command.Env = append(os.Environ(), "VIRFIELD_TEST_DIR="+directory, "VIRFIELD_TEST_SCOPE="+test.scope)
			out, err := command.CombinedOutput()
			listener.Close()
			<-serverDone
			if ctx.Err() != nil {
				t.Fatal("Automatic desktop probe exceeded its bound", ctx.Err())
			}
			if test.ready && (err != nil || string(out) != "desktop-ready") {
				t.Fatal("Valid worker/image readiness rejected", err, string(out))
			}
			if !test.ready {
				failure, ok := err.(*exec.ExitError)
				if !ok || failure.ExitCode() != 65 {
					t.Fatal("Unready desktop was accepted", err, string(out))
				}
			}
			proof, err := os.ReadFile(filepath.Join(directory, "desktop-result"))
			if test.marker && (err != nil || string(proof) != test.scope+"\nfixture-boot\nRFB 003.889\n") {
				t.Fatal("Automatic desktop proof is not bound to this scope/boot", err, string(proof))
			}
			if !test.marker && !os.IsNotExist(err) {
				t.Fatal("Unexpected desktop marker", err)
			}
		})
	}
}
