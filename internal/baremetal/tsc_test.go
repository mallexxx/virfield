package baremetal

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGuestInvariantTSCRequiresStableHostClockOnEveryCPU(t *testing.T) {
	for _, test := range []struct {
		name, cpuinfo, clock, want string
	}{
		{"Intel without VMX scaling", "flags : constant_tsc nonstop_tsc\nvmx flags : tsc_offset\n", "tsc\n", "+invtsc"},
		{"scaling alone is insufficient", "flags : vmx\nvmx flags : tsc_scaling\n", "tsc\n", "-invtsc"},
		{"unstable active clock", "flags : constant_tsc nonstop_tsc\n", "hpet\n", "-invtsc"},
		{"one CPU lacks invariant TSC", "flags : constant_tsc nonstop_tsc\nflags : constant_tsc\n", "tsc\n", "-invtsc"},
		{"missing CPU flags", "", "tsc\n", "-invtsc"},
		{"missing clocksource", "flags : constant_tsc nonstop_tsc\n", "", "-invtsc"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			cpu, clock := filepath.Join(dir, "cpuinfo"), filepath.Join(dir, "clocksource")
			if err := os.WriteFile(cpu, []byte(test.cpuinfo), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(clock, []byte(test.clock), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", "-c", vmCPUFunctions+`vf_tsc_flags "$1" "$2"`, "sh", cpu, clock)
			out, err := command.CombinedOutput()
			if err != nil || string(out) != test.want {
				t.Fatalf("Unjustified guest TSC capability: %q, %v", out, err)
			}
		})
	}
}
