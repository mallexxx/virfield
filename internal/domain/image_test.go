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
		"recipe":   func(p *ImageProfile) { p.Provision = "unknown-v1" },
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

func TestTartUITestProfileValidates(t *testing.T) {
	p := ImageProfile{
		Tart:       &TartSource{Upstream: "ghcr.io/cirruslabs/macos-golden-gate-xcode@sha256:324ea5656dee8ab9b0a0df70fda2cfed8912051ad0eca3b1b883bf6ddac88fab"},
		MacOS:      "27.0",
		Build:      "26A428",
		DisableSIP: true,
		Provision:  "uitest-27-v1",
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.DisableSIP = false
	if err := p.Validate(); err == nil {
		t.Fatal("accepted a UI-test profile with SIP enabled")
	}
}

func TestNodeSourceProfile(t *testing.T) {
	p := ImageProfile{Node: &NodeSource{Golden: "sonoma-bootstrap"}, MacOS: "14.8.9", Build: "23J631", DisableSIP: true}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]ImageProfile{
		"path golden": {Node: &NodeSource{Golden: "../etc"}, MacOS: "14.8.9", Build: "23J631"},
		"two sources": {Node: p.Node, Tart: &TartSource{Upstream: "ghcr.io/a/b@sha256:" + strings.Repeat("0", 64)}, MacOS: "14.8.9", Build: "23J631", Provision: "developer-v1"},
		"provisioned": {Node: p.Node, MacOS: "14.8.9", Build: "23J631", Provision: "security-v1", Security: "automation", DisableSIP: true},
		"no build":    {Node: p.Node, MacOS: "14.8.9"},
		"with ipsw":   {Node: p.Node, MacOS: "14.8.9", Build: "23J631", URL: "https://updates.cdn-apple.com/x.ipsw"},
	} {
		if bad.Validate() == nil {
			t.Fatal(name, "accepted")
		}
	}
}

func TestTartBaseAndAutomationProfilesValidate(t *testing.T) {
	p := tartProfile()
	p.Xcode = nil
	p.Provision = "base-v1"
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Provision = "security-v1"
	p.Security = "automation"
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Registry = &RegistryReference{Source: "team", Organization: "cirruslabs", Repository: "macos-ventura-base", Tag: "stable", Digest: "sha256:9fb387bb987cdda942fbc27118296ed8e16a3359a672c95c6d5ac960165ef1c5", Size: 1024}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Registry.Digest = "sha256:" + strings.Repeat("a", 64)
	if p.Validate() == nil {
		t.Fatal("unmatched registry provenance accepted")
	}
}
