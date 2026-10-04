package lessons_test

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/learn"
	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

// TestSetupCommandsMatchLearn keeps the setup check in step with the
// commands internal/learn runs.
func TestSetupCommandsMatchLearn(t *testing.T) {
	a := slices.Clone(lessons.SetupCommands)
	b := slices.Clone(learn.Commands)
	slices.Sort(a)
	slices.Sort(b)
	if !slices.Equal(a, b) {
		t.Fatalf("lessons.SetupCommands %v, learn.Commands %v", a, b)
	}
}
