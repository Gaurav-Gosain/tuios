package serverscope_test

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper.
//
// This has to be an external test package (serverscope_test, not serverscope):
// testutil imports serverscope for its own socket-path budget, so an in-package
// test file importing testutil back would be a cycle.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
