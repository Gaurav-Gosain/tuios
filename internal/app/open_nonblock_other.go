//go:build !unix

package app

// openNonblock is zero where the platform has no O_NONBLOCK. There the check
// before the open is what keeps a pipe out.
const openNonblock = 0

// openNoFollow is zero where the platform has no O_NOFOLLOW.
const openNoFollow = 0
