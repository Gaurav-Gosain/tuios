package input

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories.
//
// Constructing an app.OS loads (and, when absent, writes) the user config, and
// several overlays read and write tape and layout files. See
// testutil.RunIsolated for why this cannot be a per-test helper.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
