package app

import "testing"

// newTestKitty is NewKittyPassthroughWithOptions for a test. The passthrough
// starts a frame writer on the first video frame, and the test's end stops it,
// as a client's Cleanup does.
func newTestKitty(t testing.TB, opts KittyPassthroughOptions) *KittyPassthrough {
	t.Helper()
	kp := NewKittyPassthroughWithOptions(opts)
	t.Cleanup(kp.stopAsyncWriter)
	return kp
}
