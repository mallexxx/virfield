package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestShowDesktopPrintsOrOpensTheURL(t *testing.T) {
	tunnel := domain.Tunnel{LeaseID: "lease-a", Service: "desktop", URL: "vnc://admin:pw@127.0.0.1:5901"}
	var out bytes.Buffer
	opened := ""
	open := func(url string) error { opened = url; return nil }
	if err := showDesktop(tunnel, true, &out, open); err != nil || out.String() != tunnel.URL+"\n" || opened != "" {
		t.Fatal(out.String(), opened, err)
	}
	out.Reset()
	if err := showDesktop(tunnel, false, &out, open); err != nil || opened != tunnel.URL || out.Len() != 0 {
		t.Fatal(out.String(), opened, err)
	}
	if err := showDesktop(domain.Tunnel{URL: "http://x"}, true, &out, open); err == nil {
		t.Fatal("accepted a non-VNC URL")
	}
	if err := showDesktop(tunnel, false, &out, func(string) error { return errors.New("no GUI") }); err == nil {
		t.Fatal("open failure ignored")
	}
}
