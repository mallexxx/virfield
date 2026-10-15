package guestssh

import (
	"context"
	"io"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// waitScreenSharing verifies the VM's actual protocol through its authenticated
// SSH connection. It changes no guest preferences or credentials. The context
// closes the owning transport even if channel creation has not been acknowledged;
// forwarded SSH connections do not implement socket deadlines.
func (g *Client) waitScreenSharing(ctx context.Context, timeout time.Duration) error {
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stop := context.AfterFunc(wait, g.Close)
	defer stop()
	failure := domain.Err("lease_desktop_failed", "Guest Screen Sharing did not return a complete RFB greeting within the preparation budget")
	for wait.Err() == nil {
		connection, err := g.client.Dial("tcp", "127.0.0.1:5900")
		if err == nil {
			var greeting [12]byte
			_, err = io.ReadFull(connection, greeting[:])
			_ = connection.Close()
			if wait.Err() != nil {
				return failure
			}
			if err == nil {
				if !rfbGreeting(greeting[:]) {
					return failure
				}
				stop()
				if wait.Err() != nil {
					return failure
				}
				return nil
			}
		}
		// A failed connection or an early close can precede launchd startup.
		// Retry only this read-only local service probe, within one deadline.
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-wait.Done():
			timer.Stop()
			return failure
		case <-timer.C:
		}
	}
	return failure
}

func rfbGreeting(b []byte) bool {
	if len(b) != 12 || string(b[:4]) != "RFB " || b[7] != '.' || b[11] != '\n' {
		return false
	}
	for _, i := range []int{4, 5, 6, 8, 9, 10} {
		if b[i] < '0' || b[i] > '9' {
			return false
		}
	}
	return true
}
