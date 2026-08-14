package harness

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper. It matters here in particular: UserDir reads XDG_CONFIG_HOME, so an
// unisolated run would load whatever manifests the developer happens to have.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
