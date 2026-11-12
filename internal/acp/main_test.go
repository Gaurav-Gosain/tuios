package acp

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories, as every tuios test package does. The package itself reads no
// XDG path; this file goes when the package becomes a library of its own.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
