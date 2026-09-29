package images

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDesktopDoesNotHideLoginAssistantBehindFinder(t *testing.T) {
	observations := 0
	got, err := waitDesktop(context.Background(), time.Millisecond, time.Hour, func(context.Context) (string, error) {
		observations++
		if observations == 1 {
			return "desktop\n", nil
		}
		return "assistant\n", nil
	})
	if err != nil || got != "assistant" || observations != 2 {
		t.Fatal(got, err, observations)
	}
}

func TestDesktopRequiresSettledFinderAndPreservesReadFailures(t *testing.T) {
	observations := 0
	got, err := waitDesktop(context.Background(), time.Millisecond, 3*time.Millisecond, func(context.Context) (string, error) { observations++; return "desktop\n", nil })
	if err != nil || got != "desktop" || observations < 2 {
		t.Fatal(got, err, observations)
	}
	failure := errors.New("guest disconnected")
	_, err = waitDesktop(context.Background(), time.Millisecond, time.Hour, func(context.Context) (string, error) { return "", failure })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = waitDesktop(ctx, time.Hour, time.Hour, func(context.Context) (string, error) { return "waiting", nil })
	if err == nil {
		t.Fatal("canceled desktop wait succeeded")
	}
}
