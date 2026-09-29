package lume

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// Recovery runs exactly one owned Lume recovery process. Only the versioned
// guest driver receives its VNC endpoint. It must shut down the guest after
// csrutil commits; absence of a verified stopped state is an ambiguous failure.
func (c *Client) Recovery(ctx context.Context, binary, folder string, l domain.Lease, drive func(context.Context, string) error) error {
	if !domain.ValidName(l.VMName) || !domain.ValidName(l.Location) || !filepath.IsAbs(binary) {
		return domain.Err("invalid_profile", "invalid Recovery identity")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return err
	}
	password := domain.NewID("")[:8]
	owned, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(owned, binary, "run", l.VMName, "--storage", l.Location, "--no-display", "--recovery-mode", "true", "--vnc-port", strconv.Itoa(port), "--vnc-password", password)
	done := make(chan error, 1)
	go func() { done <- RunPrivate(owned, cmd, filepath.Join(folder, "recovery-process.log")) }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			<-done
		}
	}()
	endpoint := "vnc://:" + password + "@127.0.0.1:" + strconv.Itoa(port)
	ready, cancelReady := context.WithTimeout(ctx, 60*time.Second)
	defer cancelReady()
	for {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ready, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			conn.Close()
			break
		}
		select {
		case <-ready.Done():
			return domain.Err("recovery_unavailable", "Recovery VNC did not become available; inspect the private process log")
		case err := <-done:
			finished = true
			if err != nil {
				return err
			}
			return domain.Err("recovery_unavailable", "Recovery exited before VNC became available")
		case <-time.After(time.Second):
		}
	}
	// A fresh private port belongs to this subprocess; never attach to a stale VM
	// session endpoint from a previous boot. The driver waits for its framebuffer.
	if err := drive(owned, endpoint); err != nil {
		return err
	}
	stopped, cancelWait := context.WithTimeout(ctx, 45*time.Second)
	defer cancelWait()
	exited := done
	for {
		o, err := c.Observe(stopped)
		if err == nil {
			for _, v := range o.VMs {
				if v.Key() == l.Key() && v.State == "stopped" {
					cancel()
					if !finished {
						<-done
						finished = true
					}
					return nil
				}
			}
		}
		select {
		case <-stopped.Done():
			return domain.Err("recovery_stop_unconfirmed", "Recovery did not reach stopped state after signed SIP change; inspect before retry")
		case <-exited:
			finished = true
			exited = nil
			// The driver has already verified csrutil's successful commit. Wait
			// for fresh inventory even when process exit races state publication.
		case <-time.After(time.Second):
		}
	}
}
