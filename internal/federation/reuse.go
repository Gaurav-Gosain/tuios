package federation

import "strings"

const staleMasterPrefix = "Control socket connect("

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
