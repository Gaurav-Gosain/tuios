//go:build slim

package main

// runTestChild: tuios-slim has no mcp command, so no test starts a child.
func runTestChild() (int, bool) { return 0, false }
