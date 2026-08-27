package federation

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. This package never reads them itself, but a link test runs a
// real child process, and a child that inherits the real tree could reach the
// developer's daemon socket. See testutil.RunIsolated.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
