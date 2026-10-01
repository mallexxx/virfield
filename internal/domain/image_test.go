package domain

import (
	"strings"
	"testing"
)

func TestRecoveryPoliciesAcrossAppleSiliconGenerations(t *testing.T) {
	for _, version := range []string{"11.7", "12.6", "13.7", "14.8", "15.7", "26.4", "27.0"} {
		p := ImageProfile{MacOS: version, Build: "testbuild", URL: "https://updates.cdn-apple.com/test.ipsw", SHA256: strings.Repeat("a", 64), Size: 123, DisableSIP: true, Security: "automation", Provision: "security-v1"}
		if err := p.Validate(); err != nil {
			t.Fatalf("%s: %v", version, err)
		}
		p.DisableSIP = false
		if p.Validate() == nil {
			t.Fatal("Automation silently kept SIP enabled")
		}
		p.DisableSIP = true
		p.Provision = ""
		if p.Validate() == nil {
			t.Fatal("Automation silently skipped provisioning")
		}
	}
	for _, version := range []string{"", "10.15", "16", "28", "12;echo bad"} {
		if SupportsRecovery(version) {
			t.Fatal(version)
		}
	}
}
