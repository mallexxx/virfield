package images

import (
	"errors"
	"github.com/mallexxx/virfield/internal/domain"
	"testing"
)

func TestGoldenDiskPolicy(t *testing.T) {
	for _, tt := range []struct {
		name       string
		output     string
		code       string
		legacyUUID bool
	}{
		{"unencrypted", "false\nfalse\nfalse\nfalse\nfalse\nfalse\n", "", false},
		// Captured from a real Monterey guest after native Setup Assistant: FV off
		// on both volumes, but encrypted at rest. This must not pass publication.
		{"monterey encryption at rest", "false\ntrue\nfalse\nfalse\ntrue\nfalse\n", "disk_encrypted", false},
		{"legacy encryption at rest", "false\ntrue\nfalse\nfalse\ntrue\nfalse\n", "", true},
		{"legacy filevault still rejected", "false false false true true false", "disk_encrypted", true},
		{"legacy locked still rejected", "false false false false true true", "disk_locked", true},
		{"data only encrypted", "false\nfalse\nfalse\nfalse\ntrue\nfalse\n", "disk_encrypted", false},
		{"filevault", "false false false true true false", "disk_encrypted", false},
		{"locked", "false false false false false true", "disk_locked", false},
		{"missing property", "false false false false false", "disk_policy_unknown", false},
		{"unrecognized value", "false false false false unknown false", "disk_policy_unknown", false},
		{"extra output", "false false false false false false warning", "disk_policy_unknown", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			disk, err := parseDiskEvidence(tt.output)
			if err == nil {
				err = disk.validate(tt.legacyUUID)
			}
			if tt.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *domain.Error
			if !errors.As(err, &failure) || failure.Code != tt.code {
				t.Fatalf("want %s, got %v", tt.code, err)
			}
		})
	}
}
