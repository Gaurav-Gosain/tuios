//go:build !unix

package session

// processUmask is 0 where the platform has no umask. A copy from another
// machine still loses group and world write, which commitMode clears on its
// own.
var processUmask uint32
