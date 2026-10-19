//go:build !linux && !darwin && !windows

package transcriptview

// serverOwner cannot read a socket table here, so the password is never
// sent and the export is read instead.
func serverOwner(serverSide, int) owner { return ownerOther }
