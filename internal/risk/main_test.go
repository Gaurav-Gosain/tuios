package risk

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper. The rules read nothing from disk, but the suite holds every package
// to the same rule.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
