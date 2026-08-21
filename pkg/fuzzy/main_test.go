package fuzzy

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// The matcher itself reads nothing from disk, but every test binary in this
// tree redirects the XDG tree before its first test runs, and the next test
// written here is the one that forgets. See testutil.RunIsolated.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
