package session

import (
	"fmt"
	"io"
	"strings"
)

// ReportBeforeStart writes what a daemon that starts now brings back, and
// returns how many saved sessions there are. tuios attach calls it when no
// daemon runs, before it starts one: afterwards the sessions simply exist,
// and the user is left to guess where they came from. An unnamed attach with
// nothing saved opens a new session, and it says so.
func ReportBeforeStart(w io.Writer, sessionName string) int {
	infos, err := ListResurrectableInfos()
	if err != nil || len(infos) == 0 {
		if sessionName == "" {
			_, _ = fmt.Fprintln(w, "No saved sessions to restore. Opening a new one.")
		}
		return 0
	}
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
	}
	noun := "sessions"
	if len(names) == 1 {
		noun = "session"
	}
	shown := names
	if len(shown) > 12 {
		shown = append(shown[:12:12], fmt.Sprintf("and %d more", len(names)-12))
	}
	_, _ = fmt.Fprintf(w, "Restoring %d saved %s: %s.\n", len(names), noun, strings.Join(shown, ", "))
	_, _ = fmt.Fprintf(w, "%s: %s.\n", RestoredTag, RestoredNote)
	return len(names)
}
