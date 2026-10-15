package guestssh

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"golang.org/x/crypto/ssh"
)

func desktopGuest(t *testing.T, forward func(ssh.NewChannel)) *Client {
	t.Helper()
	port, _ := sshServerWithForward(t, "VirtualMac2,1", "lume", forward)
	m, l := testImageGuest(t, port)
	g, err := m.Connect(context.Background(), l, "127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g
}

func desktopChannel(incoming ssh.NewChannel) (ssh.Channel, error) {
	var destination struct {
		Host       string
		Port       uint32
		Origin     string
		OriginPort uint32
	}
	if ssh.Unmarshal(incoming.ExtraData(), &destination) != nil || destination.Host != "127.0.0.1" || destination.Port != 5900 {
		_ = incoming.Reject(ssh.Prohibited, "unexpected destination")
		return nil, errors.New("unexpected destination")
	}
	ch, reqs, err := incoming.Accept()
	if err == nil {
		go ssh.DiscardRequests(reqs)
	}
	return ch, err
}

func TestDesktopReadinessWaitsForFragmentedGreetingAndKeepsSSH(t *testing.T) {
	g := desktopGuest(t, func(incoming ssh.NewChannel) {
		ch, err := desktopChannel(incoming)
		if err != nil {
			return
		}
		defer ch.Close()
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(ch, "RFB ")
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(ch, "003.889\n")
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := g.waitScreenSharing(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	cancel()
	// Only the completed probe channel closes. Detach its cancellation before
	// continuing the credential-isolation verification on this SSH transport.
	if out, err := g.Run(context.Background(), "/usr/sbin/sysctl -n hw.model", ""); err != nil || out != "VirtualMac2,1\n" {
		t.Fatal("Completed desktop probe closed the provisioning transport", out, err)
	}
}

func TestDesktopReadinessBoundsMissingBannerAndChannelAcknowledgement(t *testing.T) {
	for _, mode := range []string{"no-banner", "no-channel-ack"} {
		t.Run(mode, func(t *testing.T) {
			g := desktopGuest(t, func(incoming ssh.NewChannel) {
				if mode == "no-channel-ack" {
					return
				}
				ch, err := desktopChannel(incoming)
				if err != nil {
					return
				}
				defer ch.Close()
				_, _ = io.Copy(io.Discard, ch)
			})
			started := time.Now()
			err := g.waitScreenSharing(context.Background(), 50*time.Millisecond)
			var failure *domain.Error
			if time.Since(started) > time.Second || !errors.As(err, &failure) || failure.Code != "lease_desktop_failed" {
				t.Fatal("Unready desktop escaped the bounded probe", err)
			}
		})
	}
}

func TestDesktopReadinessRejectsInvalidProtocol(t *testing.T) {
	g := desktopGuest(t, func(incoming ssh.NewChannel) {
		ch, err := desktopChannel(incoming)
		if err != nil {
			return
		}
		defer ch.Close()
		_, _ = io.WriteString(ch, "not RFB 123\n")
	})
	if err := g.waitScreenSharing(context.Background(), time.Second); err == nil {
		t.Fatal("Non-RFB service was accepted as a desktop")
	}
}
