package main

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper. It matters more here than in most packages: these tests drive the
// real app, which persists the sidebar's state, and a run that escaped would
// leave the developer's rail collapsed.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
