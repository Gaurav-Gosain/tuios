package tfx

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the XDG tree so this package's tests cannot touch the
// developer's own directories. See testutil.RunIsolated for why this cannot be
// a per-test helper.
//
// This engine reads no files and writes none, so the isolation is belt and
// braces. It is here anyway because the guard that checks for it is meant to be
// blanket: a package that argues its way out of it is how the guard stops being
// worth having. Note this is the only reach from pkg into internal, it is in a
// test file, and it therefore does not follow anyone importing this package as
// a library.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
