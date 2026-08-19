package transcript

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. It matters more here than anywhere else: this package reads the
// on-disk records agent CLIs keep, and the developer's real ones hold their
// actual conversations.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
