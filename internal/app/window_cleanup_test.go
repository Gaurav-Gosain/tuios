package app

import "testing"

// closeWindowsAtEnd closes the windows m holds when the test ends. A daemon
// window runs an output writer and a render coalescer until it is closed, and
// a model built by hand has no Cleanup that would close them. TestMain fails
// the package when any are still running after the last test.
func closeWindowsAtEnd(t testing.TB, m *OS) {
	t.Helper()
	t.Cleanup(func() {
		for _, w := range m.Windows {
			w.Close()
		}
	})
}
