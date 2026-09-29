package guestssh

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestConnectionReadinessRetriesOnlyTCPFailures(t *testing.T) {
	attempts := 0
	expected := &Client{}
	got, err := waitForConnection(context.Background(), time.Millisecond, func() (*Client, error) {
		attempts++
		if attempts < 3 {
			return nil, domain.Err("guest_connect_failed", "temporarily unavailable")
		}
		return expected, nil
	})
	if err != nil || got != expected || attempts != 3 {
		t.Fatal(got, err, attempts)
	}
	for _, code := range []string{"guest_identity_failed", "guest_authentication_failed", "guest_command_failed"} {
		attempts = 0
		_, err = waitForConnection(context.Background(), time.Millisecond, func() (*Client, error) { attempts++; return nil, domain.Err(code, "permanent failure") })
		if err == nil || attempts != 1 {
			t.Fatal("unsafe retry", code, attempts)
		}
	}
}

func TestConnectionReadinessStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	_, err := waitForConnection(ctx, time.Hour, func() (*Client, error) {
		attempts++
		cancel()
		return nil, domain.Err("guest_connect_failed", "temporary")
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatal(err, attempts)
	}
}
