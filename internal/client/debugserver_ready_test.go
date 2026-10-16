package client

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// This child deliberately exists before listening, just like Apple's debugserver
// while it launches and attaches to the inferior. Its first connection is the
// debugger: a readiness TCP connection must not consume it.
func TestDebugserverDelayedListenerProcess(t *testing.T) {
	if os.Getenv("VIRFIELD_DEBUGSERVER_FIXTURE") != "1" {
		return
	}
	fmt.Println("process-started")
	time.Sleep(400 * time.Millisecond)
	listener, err := net.Listen("tcp", os.Getenv("VIRFIELD_DEBUGSERVER_ADDRESS"))
	if err != nil {
		os.Exit(71)
	}
	defer listener.Close()
	connection, err := listener.Accept()
	if err != nil {
		os.Exit(72)
	}
	_, _ = io.WriteString(connection, "first-debugger-connection")
	connection.Close()
	os.Exit(0)
}

func TestDebugserverPrepareWaitsForOwnListenerWithoutConnecting(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Generated macOS guest helper uses Apple's lsof")
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	port := reservation.Addr().(*net.TCPAddr).Port
	reservation.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "debugserver")
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, data, 0700); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(fixture, "-test.run=^TestDebugserverDelayedListenerProcess$")
	child.Env = append(os.Environ(), "VIRFIELD_DEBUGSERVER_FIXTURE=1", "VIRFIELD_DEBUGSERVER_ADDRESS="+address)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "process-started\n" {
		t.Fatal("Fixture did not reach the pre-listen state", line, err)
	}
	start := time.Now()
	wait := exec.Command("/bin/bash", "-c", "debugserver_pid=$1\nport=$2\n"+remoteDebugWaitScript(), "wait", strconv.Itoa(child.Process.Pid), strconv.Itoa(port))
	if out, err := wait.CombinedOutput(); err != nil {
		t.Fatalf("Listener readiness failed: %v: %s", err, out)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Fatal("Preparation returned while debugserver existed but its port was not listening")
	}
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal("Preparation did not leave the first connection for LLDB", err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	reply, err := io.ReadAll(connection)
	if err != nil || string(reply) != "first-debugger-connection" {
		t.Fatal("Readiness consumed the debugger's connection", string(reply), err)
	}
}
