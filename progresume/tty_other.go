//go:build !unix && !windows

package progresume

import (
	"errors"
	"io"
)

// openTTY reports that this system has no controlling terminal to write a
// report to.
func openTTY() (io.Writer, error) {
	return nil, errors.New("progresume: this system has no controlling terminal")
}
