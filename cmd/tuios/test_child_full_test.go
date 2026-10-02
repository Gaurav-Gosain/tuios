//go:build !slim

package main

import "os"

// runTestChild runs the tuios mcp child a test started, when this binary is
// one. See mcp_command_test.go.
func runTestChild() (int, bool) {
	if os.Getenv("TUIOS_TEST_MCP_CHILD") == "1" {
		return runMCPChild(), true
	}
	return 0, false
}
