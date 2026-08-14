// Package fuzz is the target-agnostic half of tuios's property fuzzer: an
// action alphabet, a seeded generator that biases toward the shapes that have
// actually broken this codebase, a driver loop, and a shrinker.
//
// It knows nothing about tuios itself. A caller supplies a Target, which
// applies one Action and reports which invariants the resulting state violates,
// and the loop here handles seeding, reproduction, and minimisation. Two
// targets exist: an in-process one that drives app.OS through its real
// bubbletea Update and reads the invariants off the model (internal/app), and a
// tuitest one that replays the same stream against a real binary in a PTY
// (e2e/tui). The alphabet is shared so a failure found by either is a repro the
// other can also run.
//
// Nothing in the shipped binary imports this package; it exists for `go test`.
package fuzz

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind is one action in the alphabet. The set covers what a user does and, more
// to the point, what has broken: the leader chords, drags that end outside the
// target they started on, host resizes landing mid-gesture, and the runtime
// settings flips that make a pane's border appear under a guest that was never
// told about it.
type Kind uint8

const (
	Key             Kind = iota // a single key press, S is the key name
	Chord                       // the leader key then S, as two presses
	Text                        // S typed a rune at a time, for rename fields
	MousePress                  // A,B is the cell, C is the button
	MouseMotion                 // A,B is the cell, C is the button held (0 = none)
	MouseRelease                // A,B is the cell, C is the button
	MouseWheel                  // A,B is the cell, C is the direction
	Resize                      // A,B is the new host size in cells
	NewPane                     //
	ClosePane                   // A selects which pane, modulo the count
	ZoomPane                    //
	FocusPane                   // A selects which pane, modulo the count
	MovePane                    // A is a direction index
	SwitchWorkspace             // A is the workspace, 1..NumWorkspaces
	SwitchSession               // A selects a session, modulo the count
	ToggleTiling                //
	ToggleShared                // shared borders
	LayoutMode                  // A selects bsp/master-stack/scrolling
	ToggleSidebar               //
	SidebarCollapse             //
	SidebarPosition             // A picks left or right
	OpenOverlay                 // A selects which overlay
	CloseOverlay                //
	Rename                      // S is the new name
	Detach                      //
	Attach                      //
	Setting                     // A selects a runtime setting, B its new value
	Tick                        // one maintenance tick
	Guest                       // S is written to the focused pane's emulator
	AltScreen                   // A odd enters the alternate screen, B picks the pane
	Burst                       // A lines out of the pane B picks, to outrun a buffer
	SecondClient                // a second client attaches to the same live session
	DaemonRestart               // the daemon goes away and its sessions come back
	kindCount
)

var kindNames = [kindCount]string{
	Key: "key", Chord: "chord", Text: "text",
	MousePress: "press", MouseMotion: "motion", MouseRelease: "release", MouseWheel: "wheel",
	Resize: "resize", NewPane: "new-pane", ClosePane: "close-pane", ZoomPane: "zoom",
	FocusPane: "focus", MovePane: "move", SwitchWorkspace: "workspace", SwitchSession: "session",
	ToggleTiling: "tiling", ToggleShared: "shared-borders", LayoutMode: "layout",
	ToggleSidebar: "sidebar", SidebarCollapse: "sidebar-collapse", SidebarPosition: "sidebar-pos",
	OpenOverlay: "overlay-open", CloseOverlay: "overlay-close", Rename: "rename",
	Detach: "detach", Attach: "attach", Setting: "setting", Tick: "tick", Guest: "guest",
	AltScreen: "altscreen", Burst: "burst",
	SecondClient: "second-client", DaemonRestart: "daemon-restart",
}

func (k Kind) String() string {
	if k < kindCount && kindNames[k] != "" {
		return kindNames[k]
	}
	return "kind" + strconv.Itoa(int(k))
}

// Action is one step of a run. The three integers and the string cover every
// kind, which keeps the repro format a single flat line per action and the
// shrinker's per-field simplification uniform.
type Action struct {
	Kind    Kind
	A, B, C int
	S       string
}

// Mouse buttons, matching the order the generator picks from.
const (
	ButtonNone = iota
	ButtonLeft
	ButtonRight
	ButtonMiddle
)

// String renders one action as the line the repro file carries. Strings are
// quoted with %q so a name holding a newline, a path separator, or a combining
// mark survives the round trip through a terminal and a paste buffer.
func (a Action) String() string {
	var b strings.Builder
	b.WriteString(a.Kind.String())
	switch a.Kind {
	case Key, Chord, Text, Rename, Guest:
		fmt.Fprintf(&b, " %q", a.S)
	case MousePress, MouseMotion, MouseRelease, MouseWheel:
		fmt.Fprintf(&b, " %d %d %d", a.A, a.B, a.C)
	case Resize, Setting, AltScreen, Burst:
		fmt.Fprintf(&b, " %d %d", a.A, a.B)
	case ClosePane, FocusPane, MovePane, SwitchWorkspace, SwitchSession,
		LayoutMode, SidebarPosition, OpenOverlay:
		fmt.Fprintf(&b, " %d", a.A)
	}
	return b.String()
}

// ParseAction reads back one line of String's output.
func ParseAction(line string) (Action, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return Action{}, errSkip
	}
	name, rest, _ := strings.Cut(line, " ")
	kind := kindCount
	for k, n := range kindNames {
		if n == name {
			kind = Kind(k)
			break
		}
	}
	if kind == kindCount {
		return Action{}, fmt.Errorf("unknown action %q", name)
	}
	a := Action{Kind: kind}
	rest = strings.TrimSpace(rest)
	switch kind {
	case Key, Chord, Text, Rename, Guest:
		s, err := strconv.Unquote(rest)
		if err != nil {
			return Action{}, fmt.Errorf("%s: %w", name, err)
		}
		a.S = s
	default:
		nums := strings.Fields(rest)
		dst := []*int{&a.A, &a.B, &a.C}
		for i, f := range nums {
			if i >= len(dst) {
				break
			}
			n, err := strconv.Atoi(f)
			if err != nil {
				return Action{}, fmt.Errorf("%s: %w", name, err)
			}
			*dst[i] = n
		}
	}
	return a, nil
}

var errSkip = fmt.Errorf("blank line")

// Script renders a whole run as a pasteable repro.
func Script(as []Action) string {
	var b strings.Builder
	for _, a := range as {
		b.WriteString(a.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// ParseScript reads back Script's output, skipping blanks and # comments so a
// maintainer can annotate a saved repro.
func ParseScript(s string) ([]Action, error) {
	var out []Action
	for i, line := range strings.Split(s, "\n") {
		a, err := ParseAction(line)
		if err == errSkip {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		out = append(out, a)
	}
	return out, nil
}
