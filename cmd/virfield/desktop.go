package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mallexxx/virfield/internal/domain"
)

// showDesktop prints a desktop tunnel's URL or opens it in Screen Sharing.
func showDesktop(t domain.Tunnel, print bool, out io.Writer, open func(string) error) error {
	if !strings.HasPrefix(t.URL, "vnc://") {
		return errors.New("lease desktop tunnel returned no VNC URL")
	}
	if print {
		_, err := fmt.Fprintln(out, t.URL)
		return err
	}
	return open(t.URL)
}
