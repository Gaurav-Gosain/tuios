//go:build windows || js

package session

import (
	"net"
	"os"
)

// handoffSupported is false: a ConPTY is a pseudoconsole handle and pipe
// handles, not descriptors an exec keeps, and there is no exec in place.
func handoffSupported() bool { return false }

func listenerFD(net.Listener, func(int)) bool { return false }

func execHandoff(string, []string, []string, []int, func()) error { return ErrHandoffUnsupported }

func adoptedFile(int, string) *os.File { return nil }

func newHandoffFile(string) (*os.File, error) { return nil, ErrHandoffUnsupported }

func adoptMaster(int, string) (paneIO, error) { return nil, ErrHandoffUnsupported }
