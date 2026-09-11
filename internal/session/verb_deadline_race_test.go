//go:build race

package session

// testDeadlineScale stretches the test client's socket deadlines under the race
// detector.
//
// The nightly race build killed TestStashTransferIsBounded at 8.26 seconds
// against a 5 second read deadline. That test hands the daemon an 8 MiB payload
// base64 encoded to about 11 MB, and instrumented reads and writes of that much
// data take longer than the number a developer picked while watching an
// uninstrumented run. The bound being tested is the daemon's, in bytes, and it
// does not move; only the wall clock around it does.
const testDeadlineScale = 8
