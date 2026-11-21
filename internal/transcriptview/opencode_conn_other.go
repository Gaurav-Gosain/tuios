//go:build !linux && !darwin && !windows

package transcriptview

func serverOwner(serverSide, int) owner { return ownerOther }
