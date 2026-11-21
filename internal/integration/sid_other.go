//go:build !unix

package integration

func selfSID() int { return 0 }
