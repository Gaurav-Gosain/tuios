package tuios_test

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories, and fails the run if anything reached them anyway. Constructing
// a model loads the user config, and writes a default one when there is none.
// See testutil.RunIsolated for why this cannot be a per-test helper.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
