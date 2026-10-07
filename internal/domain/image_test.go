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

func tartProfile() ImageProfile {
	return ImageProfile{
		Tart:       &TartSource{Upstream: "ghcr.io/cirruslabs/macos-ventura-base@sha256:9fb387bb987cdda942fbc27118296ed8e16a3359a672c95c6d5ac960165ef1c5"},
		MacOS:      "13.7.4",
		Build:      "22H420",
		Security:   "sip-disabled",
		DisableSIP: true,
		Provision:  "developer-v1",
		Xcode:      &XcodeRelease{Version: "15.2", Build: "15C500b", Requires: "13.5", URL: "https://download.developer.apple.com/Developer_Tools/Xcode_15.2/Xcode_15.2.xip", SHA1: "3f1e6943264ba640f83e655768df439902acc406"},
	}
}

func TestTartProfileValidates(t *testing.T) {
	if err := tartProfile().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTartProfileRejectsUnpinnedOrCompetingSources(t *testing.T) {
	cases := map[string]func(*ImageProfile){
		"tag": func(p *ImageProfile) { p.Tart.Upstream = "ghcr.io/cirruslabs/macos-ventura-base:latest" },
		"host": func(p *ImageProfile) {
			p.Tart.Upstream = "docker.io/cirruslabs/macos-ventura-base@sha256:" + strings.Repeat("a", 64)
		},
		"short":    func(p *ImageProfile) { p.Tart.Upstream = "ghcr.io/cirruslabs/x@sha256:abc" },
		"ipsw":     func(p *ImageProfile) { p.URL = "https://updates.cdn-apple.com/x.ipsw" },
		"no build": func(p *ImageProfile) { p.Build = "" },
		"no xcode": func(p *ImageProfile) { p.Xcode = nil; p.Provision = "" },
		"recipe":   func(p *ImageProfile) { p.Provision = "security-v1"; p.Security = "automation"; p.Xcode = nil },
		"registry": func(p *ImageProfile) { p.Registry = &RegistryReference{} },
	}
	for name, mutate := range cases {
		p := tartProfile()
		mutate(&p)
		if err := p.Validate(); err == nil {
			t.Fatalf("%s: accepted invalid Tart profile", name)
		}
	}
}
