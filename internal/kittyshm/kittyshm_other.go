//go:build !darwin

package kittyshm

import (
	"bytes"
	"errors"
	"os"

	"tuios.dev/vt"
)

// Available reports whether this machine reaches a shared memory object by name.
// On Linux it is a file under /dev/shm. Elsewhere tuios does not reach one.
func Available() bool { return false }

// Object is a shared memory object opened read-only. Open returns none here.
type Object struct{ *bytes.Reader }

// Stat describes the object.
func (o *Object) Stat() os.FileInfo { return nil }

var errNoObject = errors.New("shared memory objects are not reached by name here")

// Stat is not available on this machine.
func Stat(string) (os.FileInfo, error) { return nil, errNoObject }

// Open is not available on this machine.
func Open(string, int64) (*Object, error) { return nil, errNoObject }

// Close is not reached on this machine: Open returns no object.
func (o *Object) Close() error { return nil }

// ReleaseFrame does nothing on this machine.
func ReleaseFrame(*vt.KittyCommand) {}
