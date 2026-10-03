package lume

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// Recovery runs exactly one owned Lume recovery process. Only the versioned
// guest driver receives its VNC endpoint. It must shut down the guest after
// csrutil commits; absence of a verified stopped state is an ambiguous failure.
func (c *Client) Recovery(ctx context.Context, binary, folder, sessionPath string, l domain.Lease, drive func(context.Context, string) error) error {
	if !domain.ValidName(l.VMName) || !domain.ValidName(l.Location) || !filepath.IsAbs(binary) || !filepath.IsAbs(sessionPath) || filepath.Base(sessionPath) != "sessions.json" {
		return domain.Err("invalid_profile", "invalid Recovery identity")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	// Lume asks the kernel for an available port and writes the actual endpoint
	// only after its VNC server is bound. Remove a stale session before launch.
	if err := os.Remove(sessionPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	password := domain.NewID("")[:8]
	secret, err := os.CreateTemp(folder, "vnc-password-")
	if err != nil {
		return err
	}
	defer os.Remove(secret.Name())
	if _, err := secret.WriteString(password); err != nil {
		secret.Close()
		return err
	}
	if err := secret.Close(); err != nil {
		return err
	}
	owned, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(owned, binary, "run", l.VMName, "--storage", l.Location, "--no-display", "--recovery-mode", "true", "--vnc-port", "0", "--vnc-password-file", secret.Name())
	done := make(chan error, 1)
	go func() { done <- RunPrivate(owned, cmd, filepath.Join(folder, "recovery-process.log")) }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			<-done
		}
	}()
	ready, cancelReady := context.WithTimeout(ctx, 60*time.Second)
	defer cancelReady()
	var endpoint string
	for {
		endpoint, err = recoverySessionEndpoint(sessionPath, password)
		if err == nil {
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
	// The session was absent before this child launched. Lume wrote its actual
	// kernel-assigned port, and the private password must match this launch.
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

func recoverySessionEndpoint(path, password string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > 1024 {
		return "", domain.Err("recovery_unavailable", "Recovery VNC session is invalid")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var session struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(b, &session); err != nil {
		return "", err
	}
	u, err := url.Parse(session.URL)
	if err != nil || u.Scheme != "vnc" || u.Hostname() != "127.0.0.1" || u.User == nil || u.User.Username() != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return "", domain.Err("recovery_unavailable", "Recovery VNC session endpoint is invalid")
	}
	p, err := strconv.Atoi(u.Port())
	got, ok := u.User.Password()
	if err != nil || p < 1 || p > 65535 || !ok || got != password {
		return "", domain.Err("recovery_unavailable", "Recovery VNC session does not belong to this launch")
	}
	return session.URL, nil
}
