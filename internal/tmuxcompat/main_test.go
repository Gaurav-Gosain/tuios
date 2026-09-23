package tmuxcompat

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper. A child started as a pane holder runs the holder instead.
func TestMain(m *testing.M) {
	runHolderIfAsked()
	os.Exit(testutil.RunIsolated(m))
}
