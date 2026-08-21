package applist

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// The launch history lands in the XDG state directory, so this binary redirects
// the whole XDG tree before any test runs. See testutil.RunIsolated for why
// this cannot be a per-test helper.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
