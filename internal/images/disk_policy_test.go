package images

import (
	"errors"
	"github.com/mallexxx/virfield/internal/domain"
	"testing"
)

func TestGoldenDiskPolicy(t *testing.T) {
	for _, tt := range []struct{ name, output, code string }{
		{"unencrypted", "false\nfalse\nfalse\nfalse\nfalse\nfalse\n", ""},
		// Captured from a real Monterey guest after native Setup Assistant: FV off
		// on both volumes, but encrypted at rest. This must not pass publication.
		{"monterey encryption at rest", "false\ntrue\nfalse\nfalse\ntrue\nfalse\n", "disk_encrypted"},
		{"data only encrypted", "false\nfalse\nfalse\nfalse\ntrue\nfalse\n", "disk_encrypted"},
		{"filevault", "false false false true true false", "disk_encrypted"},
		{"locked", "false false false false false true", "disk_locked"},
		{"missing property", "false false false false false", "disk_policy_unknown"},
		{"unrecognized value", "false false false false unknown false", "disk_policy_unknown"},
		{"extra output", "false false false false false false warning", "disk_policy_unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			disk, err := parseDiskEvidence(tt.output)
			if err == nil {
				err = disk.validate()
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
