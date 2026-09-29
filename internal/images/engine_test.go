package images

import (
	"context"
	"testing"

	"github.com/mallexxx/virfield/internal/domain"
)

func TestUnsupportedRecipeCannotStartAnyStage(t *testing.T) {
	p := profile("media")
	p.Build = "26B100"
	e := &Engine{} // No dependencies: recipe rejection must precede all effects.
	for _, step := range []string{"download", "create", "setup", "assistant", "sip", "verify", "stop"} {
		err := e.Step(context.Background(), domain.Lease{}, p, step, nil)
		failure, ok := err.(*domain.Error)
		if !ok || failure.Code != "unsupported_image" {
			t.Fatalf("%s: %v", step, err)
		}
	}
}
