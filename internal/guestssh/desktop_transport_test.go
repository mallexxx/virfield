package guestssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/qualify"
	"golang.org/x/crypto/ssh"
)

func TestDesktopTraceDistinguishesStalledSSHStages(t *testing.T) {
	for _, stage := range []string{"opening_session", "exec_request", "waiting_exit"} {
		t.Run(stage, func(t *testing.T) {
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			host, err := ssh.NewSignerFromKey(key)
			if err != nil {
				t.Fatal(err)
			}
			config := &ssh.ServerConfig{NoClientAuth: true}
			config.AddHostKey(host)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				peer, channels, requests, err := ssh.NewServerConn(raw, config)
				if err != nil {
					return
				}
				defer peer.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if stage == "opening_session" {
						_ = peer.Wait()
						return
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					defer channel.Close()
					for request := range requests {
						if request.Type == "exec" {
							if stage == "waiting_exit" {
								_ = request.Reply(true, nil)
							}
							_ = peer.Wait()
							return
						}
						_ = request.Reply(false, nil)
					}
				}
			}()
			raw, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			connection, channels, requests, err := ssh.NewClientConn(raw, listener.Addr().String(), &ssh.ClientConfig{
				User: "fixture", HostKeyCallback: ssh.FixedHostKey(host.PublicKey()),
			})
			if err != nil {
				t.Fatal(err)
			}
			guest := &Client{client: ssh.NewClient(connection, channels, requests), raw: withoutDeadlines{raw}, diagnosticDir: t.TempDir()}
			defer guest.Close()
			_, err = guest.RunReader(context.Background(), time.Second, qualify.DesktopProbe, strings.NewReader("private-fixture-input"))
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("SSH stage lost its cancellation cause", err)
			}
			path := filepath.Join(guest.diagnosticDir, "desktop-command.log")
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), "phase="+stage+" ") || !strings.Contains(string(data), "context_error=context deadline exceeded") {
				t.Fatal("Missing exact stalled transport stage", string(data), err)
			}
			if strings.Contains(string(data), "private-fixture-input") || strings.Contains(string(data), qualify.DesktopProbe) {
				t.Fatal("Transport trace recorded command or input")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("Transport trace is not private", err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Canceled SSH peer was retained")
			}
		})
	}
}

func TestDesktopProbeUsesRemainingReadinessBudget(t *testing.T) {
	old := commandTimeout
	commandTimeout = 20 * time.Millisecond
	defer func() { commandTimeout = old }()
	for _, test := range []struct {
		name, command string
		parent        time.Duration
		succeeds      bool
	}{
		{"cold desktop", qualify.DesktopProbe, time.Second, true},
		{"ordinary command stays short", "ordinary probe", time.Second, false},
		{"original deadline still wins", qualify.DesktopProbe, 10 * time.Millisecond, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			host, err := ssh.NewSignerFromKey(key)
			if err != nil {
				t.Fatal(err)
			}
			cfg := &ssh.ServerConfig{NoClientAuth: true}
			cfg.AddHostKey(host)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				peer, channels, requests, err := ssh.NewServerConn(raw, cfg)
				if err != nil {
					return
				}
				defer peer.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					defer channel.Close()
					for request := range requests {
						if request.Type != "exec" {
							_ = request.Reply(false, nil)
							continue
						}
						_ = request.Reply(true, nil)
						time.Sleep(80 * time.Millisecond)
						_, _ = io.WriteString(channel, "desktop\n")
						_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					}
				}
			}()
			raw, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			connection, channels, requests, err := ssh.NewClientConn(raw, listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.FixedHostKey(host.PublicKey())})
			if err != nil {
				raw.Close()
				t.Fatal(err)
			}
			guest := &Client{client: ssh.NewClient(connection, channels, requests), raw: withoutDeadlines{raw}, diagnosticDir: t.TempDir()}
			defer guest.Close()
			ctx, cancel := context.WithTimeout(context.Background(), test.parent)
			defer cancel()
			out, err := guest.Run(ctx, test.command, "")
			if test.succeeds && (err != nil || out != "desktop\n") {
				t.Errorf("Read-only desktop was aborted before its readiness deadline: output=%q error=%v", out, err)
			}
			if !test.succeeds && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("Command escaped its original bound: %v", err)
			}
			guest.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("SSH probe peer was not reaped")
			}
		})
	}
}
