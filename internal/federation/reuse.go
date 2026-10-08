package federation

import "strings"

// masterNameLen is the length of the name ssh's %C gives a master socket:
// a SHA-1 in hex.
const masterNameLen = 40

// reuseArgs are the options that make ssh use a live master in dir and
// otherwise connect as it always did: ControlMaster=no never opens a master
// and never fails for want of one.
func reuseArgs(dir string) []string {
	return []string{
		"-o", "ControlMaster=no",
		"-o", "ControlPath=" + dir + "/%C",
	}
}

// staleMasterPrefix starts the line ssh prints when it finds a socket in
// SharedMasterDir that no master answers on, a master that has exited:
// "Control socket connect(<path>): Connection refused". ssh then connects
// as usual, so the line says nothing about why a link failed.
const staleMasterPrefix = "Control socket connect("

// withoutStaleMasterLines removes from ssh's stderr the lines about a socket
// in SharedMasterDir that no master answers on. They are filtered rather than
// prevented: checking the socket first would need ssh's %C hash, which
// depends on the ssh version, and the master can still exit between the
// check and the connect.
func withoutStaleMasterLines(s string) string {
	dir := SharedMasterDir()
	if dir == "" || !strings.Contains(s, staleMasterPrefix) {
		return s
	}
	prefix := staleMasterPrefix + dir + "/"
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), prefix) {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
