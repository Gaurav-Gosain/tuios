package learnssh

import (
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

// challenge is a timed task. The clock starts at "Go" and stops on the
// event after which done reports true.
type challenge struct {
	ID     string
	Title  string
	Goal   string
	Hint   string
	Target time.Duration
	Setup  []lessons.Setup
	done   func(state map[string]any, e lessons.Event, mem map[string]int) bool
}

// challenges are the timed tasks, in menu order.
var challenges = []challenge{
	{
		ID:     "four-tiles",
		Title:  "Four tiles",
		Goal:   "Open four windows and tile them.",
		Hint:   "Press n four times, then t to turn tiling on.",
		Target: 20 * time.Second,
		Setup: []lessons.Setup{
			{Command: "reset"},
			{Command: "tiling", Args: []string{"off"}},
			{Command: "mode", Args: []string{"window"}},
		},
		done: func(s map[string]any, _ lessons.Event, _ map[string]int) bool {
			n, _ := s["windows"].(int)
			tiling, _ := s["tiling"].(bool)
			return n >= 4 && tiling
		},
	},
	{
		ID:     "workspace-hop",
		Title:  "Workspace hop",
		Goal:   "Put a window on workspaces 1, 2 and 3.",
		Hint:   "n opens a window. ctrl+b w 2 goes to workspace 2. alt+2 does too.",
		Target: 25 * time.Second,
		Setup: []lessons.Setup{
			{Command: "reset"},
			{Command: "mode", Args: []string{"window"}},
		},
		done: func(s map[string]any, _ lessons.Event, _ map[string]int) bool {
			used, _ := s["workspacesUsed"].([]any)
			var got []int
			for _, u := range used {
				if n, ok := u.(int); ok {
					got = append(got, n)
				}
			}
			return slices.Contains(got, 1) && slices.Contains(got, 2) && slices.Contains(got, 3)
		},
	},
	{
		ID:     "layout-dj",
		Title:  "Layout DJ",
		Goal:   "Switch to master-stack, then scrolling, then back to bsp.",
		Hint:   "ctrl+p opens the palette. Type master, then scrolling, then bsp.",
		Target: 30 * time.Second,
		Setup: []lessons.Setup{
			{Command: "reset"},
			{Command: "newWindow", Wait: 200},
			{Command: "newWindow", Wait: 200},
			{Command: "newWindow", Wait: 200},
			{Command: "mode", Args: []string{"window"}},
		},
		done: func(_ map[string]any, e lessons.Event, mem map[string]int) bool {
			if e.Type != "layout" {
				return false
			}
			want := []string{"master-stack", "scrolling", "bsp"}
			at := mem["seq"]
			if e.Data["to"] == want[at] {
				mem["seq"] = at + 1
			}
			return mem["seq"] == len(want)
		},
	},
}

func findChallenge(id string) *challenge {
	for i := range challenges {
		if challenges[i].ID == id {
			return &challenges[i]
		}
	}
	return nil
}
