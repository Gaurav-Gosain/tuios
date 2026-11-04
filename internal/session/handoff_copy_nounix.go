//go:build !unix

package session

// openNoBlock is 0 where there is no live upgrade, and no FIFO to block on.
const openNoBlock = 0
