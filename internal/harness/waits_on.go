package harness

import (
	"regexp"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// What an agent at rest still waits on.
//
// A harness can end its turn while work it started goes on: Claude Code's
// footer counts the shells and monitors left running ("1 shell, 1 monitor").
// The agent's state is done, and it is, but a reader of done takes the agent
// for finished. A [[screen.waits_on]] entry reads such a count from the screen,
// and the daemon shows it beside a done or idle state (WindowState.AgentWaitsOn).
// It never changes the state itself.

// WaitsOn is one [[screen.waits_on]] entry: a pattern whose first group is a
// count, the region it reads, and what it counts in the singular.
type WaitsOn struct {
	Region string `toml:"region"`
	Regex  string `toml:"regex"`
	What   string `toml:"what"`

	re *regexp.Regexp
}

// maxWaitsOn bounds the entries of one manifest.
const maxWaitsOn = 8

// checkWaitsOn compiles and checks the entries of a manifest's screen.
func checkWaitsOn(list []WaitsOn) error {
	if len(list) > maxWaitsOn {
		return errfmt.Errorf("%d waits_on entries, limit %d", len(list), maxWaitsOn)
	}
	for i := range list {
		w := &list[i]
		region, _, err := normalizeScreenRegion(w.Region)
		if err != nil {
			return errfmt.Errorf("waits_on %d: %w", i, err)
		}
		w.Region = region
		re, err := regexp.Compile(w.Regex)
		if err != nil {
			return errfmt.Errorf("waits_on %d: pattern %q: %w", i, w.Regex, err)
		}
		if re.NumSubexp() < 1 || w.What == "" {
			return errfmt.Errorf("waits_on %d: the pattern needs a group for the count, and what names the thing counted", i)
		}
		w.re = re
	}
	return nil
}

// WaitsOn is what the screen tail of harness id says the agent still waits
// on, such as "1 background shell", or "" when it says nothing. Each entry
// that matches adds its count, in the order the manifest lists them.
func (r *Registry) WaitsOn(id string, tail []string) string {
	m := r.Lookup(id)
	if m == nil || !m.Screen.Enabled || len(m.Screen.WaitsOn) == 0 {
		return ""
	}
	out := ""
	for _, w := range m.Screen.WaitsOn {
		if w.re == nil {
			continue
		}
		for _, line := range regionLines(tail, w.Region) {
			sub := w.re.FindStringSubmatch(line)
			if len(sub) < 2 {
				continue
			}
			n, err := strconv.Atoi(sub[1])
			if err != nil || n <= 0 {
				continue
			}
			what := w.What
			if n != 1 {
				what += "s"
			}
			if out != "" {
				out += ", "
			}
			out += strconv.Itoa(n) + " " + what
			break
		}
	}
	return out
}
