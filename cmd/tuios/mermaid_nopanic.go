//go:build !mermaidpanic && !windows && !js

package main

// framePanicAt is zero in every build but the one the end-to-end test makes
// with -tags mermaidpanic. See mermaid_panic.go.
const framePanicAt = 0
