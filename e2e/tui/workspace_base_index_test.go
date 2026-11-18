package tuie2e

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// base_index = 0 shifts the workspace number and the digit keys together. The
// workspace held in slot 1 is shown as 0 and reached with opt+0; slot 2 is
// shown as 1 and reached with opt+1. The daemon still numbers its workspaces
// from 1, so clientWorkspace reads the slot and not the label.
//
// How this could pass wrongly, written down first:
//   - The keys could shift without the labels: the pills below name both
//     workspaces through {index}, so a key-only change still fails waitPills.
//   - The labels could shift without the keys: the two alt presses check the
//     slot the client actually lands on, not the number on the pill.
//   - A switch that did nothing looks right from slot 1. The test presses
//     opt+1 first (which must leave slot 1) and opt+0 after (which must come
//     back), so a no-op fails one of the two.
//
// Negative controls: remove the call that shifts default bindings or the call
// that loads WorkspaceIndexOffset. See NEGATIVE_CONTROLS.md.
func TestWorkspaceBaseIndexZeroShiftsKeysAndNumbers(t *testing.T) {
	for _, tc := range []struct {
		name, config, first, second string
		secondKey, firstKey         rune
		firstSlot                   int
	}{
		{"zero-based", "[workspaces]\nbase_index = 0\n", "0·EDIT", "1·REVW", '1', '0', 1},
		{"custom-default", "[keybindings.workspaces]\nswitch_workspace_1 = ['alt+0']\n", "1·EDIT", "2·REVW", '2', '0', 1},
		{"custom-one-based", "[workspaces]\nbase_index = 1\n[keybindings.workspaces]\nswitch_workspace_1 = ['alt+0']\n", "1·EDIT", "2·REVW", '2', '0', 1},
		// A user key that is also another action's default resolves the same way
		// at both bases: the default keeps it, and keybinds doctor reports it.
		{"one-based-collision", "[keybindings.workspaces]\nswitch_workspace_9 = ['alt+1']\n", "1·EDIT", "2·REVW", '2', '1', 1},
		{"zero-based-collision", "[workspaces]\nbase_index = 0\n[keybindings.workspaces]\nswitch_workspace_9 = ['alt+0']\n", "0·EDIT", "1·REVW", '1', '0', 1},
		{"custom-zero-explicit", "[workspaces]\nbase_index = 0\n[keybindings.workspaces]\nswitch_workspace_1 = ['alt+1']\nswitch_workspace_2 = ['alt+0']\n", "0·EDIT", "1·REVW", '0', '1', 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const sess = "bz"
			base := t.TempDir()
			// renumberConfig sets dock_workspace_tab_format = '{index}·{name}', which
			// puts the displayed number on each pill beside a stable, unique name.
			writeConfig(t, base, tc.config+renumberConfig)
			killDaemon(t, base)
			if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
				t.Fatalf("create session: %v: %s", err, out)
			}
			// The fresh session already holds one pane on workspace 1. Add one on 2.
			if out, err := tuiosCLI(t, base, "new-window", "REVWPANE", "-s", sess, "--workspace", "2", "--no-focus"); err != nil {
				t.Fatalf("new-window on 2: %v: %s", err, out)
			}
			for ws, name := range map[string]string{"1": "EDIT", "2": "REVW"} {
				if out, err := tuiosCLI(t, base, "set-workspace-name", ws, name, "--session", sess); err != nil {
					t.Fatalf("name workspace %s: %v: %s", ws, err, out)
				}
			}

			term := attachIn(t, base, sess, startOpts{})
			// Slot 1 shows 0 and slot 2 shows 1, not 1 and 2.
			waitPills(t, term, tc.first, tc.second)
			if tc.name == "zero-based" {
				waitPills(t, term, "0:1")
			}
			if tc.name == "zero-based" && os.Getenv("TUIOS_E2E_FRAMES") != "" {
				savePNG(t, term.Screen(), hostPalette(t, ""), artifactDir(t), "workspace-0")
			}

			// secondKey must leave slot 1 for slot 2; firstKey must then reach firstSlot.
			if err := term.SendKeys(tuitest.Alt(tc.secondKey)); err != nil {
				t.Fatalf("press alt+%c: %v", tc.secondKey, err)
			}
			if got := waitClientWorkspace(t, base, 2); got != 2 {
				t.Fatalf("ASSERTION: alt+%c left the client on workspace %d, want 2\n%s", tc.secondKey, got, term.Snapshot())
			}
			if tc.name == "zero-based" {
				waitPills(t, term, "1:1")
			}
			if tc.name == "zero-based" && os.Getenv("TUIOS_E2E_FRAMES") != "" {
				if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "REVWPANE") }, uiTimeout); err != nil {
					t.Fatalf("workspace 1 pane did not appear: %v", err)
				}
				savePNG(t, term.Screen(), hostPalette(t, ""), artifactDir(t), "workspace-1-after-alt-1")
			}

			if err := term.SendKeys(tuitest.Alt(tc.firstKey)); err != nil {
				t.Fatalf("press alt+%c: %v", tc.firstKey, err)
			}
			if got := waitClientWorkspace(t, base, tc.firstSlot); got != tc.firstSlot {
				t.Fatalf("ASSERTION: alt+%c left the client on workspace %d, want %d\n%s", tc.firstKey, got, tc.firstSlot, term.Snapshot())
			}
			waitPills(t, term, tc.first, tc.second)
			openGotoPicker(t, term)
			gotoQuery(t, term, "kind:pane ws:"+tc.second[:1])
			gotoWaitRows(t, term, "numeric workspace filter must find panes in named workspaces", []string{"REVWPANE"}, nil)
			saveArtifact(t, term, artifactDir(t), "workspace-number-filter")
			sendKeys(t, term, tuitest.Esc)
			if tc.name == "zero-based" {
				sendKeys(t, term, tuitest.Ctrl('b'), "w", "1")
				if got := waitClientWorkspace(t, base, 2); got != 2 {
					t.Fatalf("workspace prefix 1 left the client on slot %d, want 2", got)
				}
				sendKeys(t, term, tuitest.Ctrl('b'), "w", "0")
				if got := waitClientWorkspace(t, base, 1); got != 1 {
					t.Fatalf("workspace prefix 0 left the client on slot %d, want 1", got)
				}
			}
			alive(t, term, "after base-index workspace switches")
		})
	}
}
