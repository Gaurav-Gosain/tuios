package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestKeybindsUnbindCommandsAreRegistered. Cobra registers by value, so a
// command declared and never added to its parent compiles, passes vet, and is
// simply absent from the binary.
//
// Negative control: leave keybindsUnbindCmd and keybindsFreeCmd out of the
// AddCommand call and this fails.
func TestKeybindsUnbindCommandsAreRegistered(t *testing.T) {
	for _, tc := range []struct {
		want string
		args []string
	}{
		{"unbind", []string{"keybinds", "unbind", "close_window", "w"}},
		{"unbind", []string{"keybinds", "unbind", "close_window"}},
		{"free", []string{"keybinds", "free", "alt+left"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd, _, err := newRootCommand().Find(tc.args)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if cmd.Name() != tc.want {
				t.Fatalf("resolved to %q, want %q", cmd.Name(), tc.want)
			}
			if err := cmd.Args(cmd, tc.args[2:]); err != nil {
				t.Errorf("%v is not an accepted argument list: %v", tc.args[2:], err)
			}
		})
	}
}

// TestUnbindHelpSaysWhichVerbIsWhich. The two commands differ in exactly the
// way a user gets wrong, so each has to point at the other.
//
// Negative control: drop the cross-reference sentence from either Long block
// and this fails.
func TestUnbindHelpSaysWhichVerbIsWhich(t *testing.T) {
	root := newRootCommand()
	unbind, _, _ := root.Find([]string{"keybinds", "unbind"})
	free, _, _ := root.Find([]string{"keybinds", "free"})

	if !strings.Contains(unbind.Long, "keybinds free") {
		t.Error("`keybinds unbind` help does not say how to free a key from every action")
	}
	if !strings.Contains(free.Long, "every scope") {
		t.Error("`keybinds free` help does not say that it acts on every scope")
	}
	// The distinction the encoding rests on has to be in the help, because it
	// is the part a reader cannot infer from the behaviour of one run.
	flat := strings.Join(strings.Fields(unbind.Long), " ")
	if !strings.Contains(flat, "gets its default back at the next load") {
		t.Error("`keybinds unbind` help does not explain why an empty list differs from a missing line")
	}
}

// TestKeybindSectionOfFindsTheTable, which is what lets the command take an
// action name alone rather than making the user name the config table too.
//
// Negative control: iterate a hardcoded subset of sections and this fails on
// the prefix or global cases.
func TestKeybindSectionOfFindsTheTable(t *testing.T) {
	cfg := config.DefaultConfig()
	for action, want := range map[string]string{
		"close_window":         config.SectionWindowManagement,
		"prefix_close_window":  config.SectionPrefixMode,
		"command_palette":      config.SectionGlobal,
		"terminal_focus_left":  config.SectionTerminalMode,
		"workspace_prefix_new": "",
	} {
		if got := keybindSectionOf(cfg, action); got != want {
			t.Errorf("keybindSectionOf(%q) = %q, want %q", action, got, want)
		}
	}
}

// TestDoctorListsADeliberateUnbind. An agent reading the report has no other
// way to tell "not mentioned" from "removed on purpose", and only one of those
// survives a reload.
//
// Negative control: drop the UNBOUND ON PURPOSE block from printKeybindReport
// and this fails.
func TestDoctorListsADeliberateUnbind(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Keybindings.UnbindAction(config.SectionWindowManagement, "close_window")
	rep := config.NewKeybindRegistry(cfg).Report(config.PaneFacts{})

	out := captureStdout(t, func() { printKeybindReport(rep) })
	if !strings.Contains(out, "UNBOUND ON PURPOSE") {
		t.Fatalf("the report has no unbound section:\n%s", out)
	}
	if !strings.Contains(out, "close_window") {
		t.Errorf("the unbound section does not name close_window:\n%s", out)
	}
}

// TestDoctorSaysNothingAboutUnboundWhenThereIsNone, so a default config's
// report does not grow a heading with nothing under it.
//
// Negative control: print the block unconditionally and this fails.
func TestDoctorSaysNothingAboutUnboundWhenThereIsNone(t *testing.T) {
	rep := config.NewKeybindRegistry(config.DefaultConfig()).Report(config.PaneFacts{})
	if out := captureStdout(t, func() { printKeybindReport(rep) }); strings.Contains(out, "UNBOUND ON PURPOSE") {
		t.Error("a default config reports an unbound section")
	}
}
