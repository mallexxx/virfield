package qualify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type failingDesktopGuest struct {
	Guest
	failure    error
	diagnostic string
}

func (g *failingDesktopGuest) Run(context.Context, string, string) (string, error) {
	return "partial-native-observation", g.failure
}

func (g *failingDesktopGuest) ReportDesktopFailure(output string) { g.diagnostic = output }

func TestDesktopRetainsOriginalProbeFailureAndPartialOutput(t *testing.T) {
	failure := errors.New("original backend connection failure")
	g := &failingDesktopGuest{failure: failure}
	if err := Desktop(context.Background(), g); !errors.Is(err, failure) {
		t.Fatal("Desktop hid the original failure", err)
	}
	for _, want := range []string{"deadline=", "probe_duration=", "partial-native-observation", failure.Error()} {
		if !strings.Contains(g.diagnostic, want) {
			t.Fatal("Missing private failure evidence", want)
		}
	}
}

func TestDesktopDoesNotHideLoginAssistantBehindFinder(t *testing.T) {
	observations := 0
	got, err := WaitDesktop(context.Background(), time.Millisecond, time.Hour, func(context.Context) (string, error) {
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
	got, err := WaitDesktop(context.Background(), time.Millisecond, 3*time.Millisecond, func(context.Context) (string, error) { observations++; return "desktop\n", nil })
	if err != nil || got != "desktop" || observations < 2 {
		t.Fatal(got, err, observations)
	}
	failure := errors.New("guest disconnected")
	_, err = WaitDesktop(context.Background(), time.Millisecond, time.Hour, func(context.Context) (string, error) { return "", failure })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = WaitDesktop(ctx, time.Hour, time.Hour, func(context.Context) (string, error) { return "waiting", nil })
	if err == nil {
		t.Fatal("canceled desktop wait succeeded")
	}
}
