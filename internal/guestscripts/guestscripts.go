// Package guestscripts holds fixed guest provisioning scripts shared by the
// Lume image engine and the Tart image recipe. Scripts take the guest user
// from the SSH session, never from a host argument.
package guestscripts

import _ "embed"

// Provision27 is the uitest-27-v1 recipe for macOS 27 UI-test goldens.
//
//go:embed provision27.sh
var Provision27 string
