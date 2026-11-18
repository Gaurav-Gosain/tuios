// Package nitpick holds go/analysis analyzers for Go code hygiene: errors
// that are dropped, development defaults read from the environment, comments
// that say what the next line says, deep nesting, and a few opt-in checks.
//
// Each rule is one plain [analysis.Analyzer], so any driver can run it: go
// vet with -vettool, a multichecker, or the nitpick command in cmd/nitpick,
// which can limit the report to the lines a change touched.
//
// Each rule also carries a measured precision: the share of its findings
// that a person judged worth a change, on a labelled sample of real Go code.
// PRECISION.md gives the method and the numbers. [Rules] lists the rules
// with their precision and whether they are on by default.
package nitpick
