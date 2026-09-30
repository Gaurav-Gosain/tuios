//go:build !linux

package learnssh

import "errors"

// sandbox is Linux only. Elsewhere the session runs without it, which is
// fine for trying the server locally and not for hosting it.
func sandbox() error { return errors.New("no sandbox on this platform") }
