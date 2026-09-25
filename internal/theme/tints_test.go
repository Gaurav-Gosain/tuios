package theme

import (
	"reflect"
	"testing"

	tint "github.com/lrstanley/bubbletint/v2"
)

// TestBuiltinTintsAreBubbletints checks the compact table against the
// bubbletint version in go.mod, tint for tint and field for field. It fails
// when bubbletint is updated and gen_tints.go was not rerun.
func TestBuiltinTintsAreBubbletints(t *testing.T) {
	def, all := builtinTints()
	want := tint.DefaultTints()
	if len(all) != len(want) {
		t.Fatalf("the table has %d tints, bubbletint %d: run go run ./internal/theme/gen_tints.go", len(all), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(all[i], want[i]) {
			t.Errorf("tint %d (%s) differs from bubbletint's: run go run ./internal/theme/gen_tints.go", i, want[i].ID)
		}
	}
	if !reflect.DeepEqual(def, tint.TintDraculaPlus) {
		t.Errorf("default tint is %v, bubbletint's is %s", def, tint.TintDraculaPlus.ID)
	}
}
