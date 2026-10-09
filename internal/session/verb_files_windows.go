//go:build windows

package session

// oNoFollow is zero on Windows, where creating a symbolic link needs a
// privilege an ordinary user lacks.
const oNoFollow = 0
