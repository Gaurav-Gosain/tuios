package main

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper.
//
// With TUIOS_TEST_MCP_CHILD set, the binary is not running tests: it is the
// tuios mcp process a test started, so the test can speak to the real command
// over real stdio. See mcp_command_test.go.
func TestMain(m *testing.M) {
	if os.Getenv("TUIOS_TEST_MCP_CHILD") == "1" {
		os.Exit(runMCPChild())
	}
	os.Exit(testutil.RunIsolated(m))
}
