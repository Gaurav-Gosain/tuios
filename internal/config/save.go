package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/pelletier/go-toml/v2"
)

func configFileHeader(configPath string) string {
	return configHeaderTop + "# Configuration location: " + configPath + "\n" + configHeaderRest
}

const configHeaderTop = "# TUIOS Configuration File\n" +
	"# This file allows you to customize appearance and keybindings\n" +
	"#\n"

const configHeaderRest = "# Documentation: https://github.com/Gaurav-Gosain/tuios\n" +
	"# For keybindings documentation, run: tuios keybinds list\n" +
	"#\n" +
	"# tuios watches this file. A save takes effect at once, with no restart.\n" +
	"# tuios does not apply a file that has an error. It keeps the settings that\n" +
	"# are in use and shows the error on screen.\n\n" +

	"# ============================================================================\n" +
	"# APPEARANCE SETTINGS\n" +
	"# ============================================================================\n" +
	"# Many of these can be changed live from the in-app settings page\n" +
	"# (open it with the leader key followed by ',').\n" +
	"#\n" +
	"# border_style: rounded, normal, thick, double, hidden, block, ascii,\n" +
	"#               outer-half-block, inner-half-block\n" +
	"# dockbar_position: bottom, top, hidden\n" +
	"# window_title_position: bottom, top, hidden\n" +
	"# window_button_style: pill, dots (macOS traffic lights)\n" +
	"# window_button_position: right, left. Independent of the style; dots and\n" +
	"#               left is the shipped pair, the macOS arrangement\n" +
	"# theme: color theme name (e.g. dracula, nord); empty for terminal colors\n" +
	"# click_to_type: single (a click on a pane starts typing in it), double\n" +
	"#               (two clicks do), off (a click only focuses)\n" +
	"# auto_enter_terminal_on_focus: off (Tab keeps cycling windows),\n" +
	"#               targeted (numbered select and arrows start typing),\n" +
	"#               all (every covered focus command, including Tab)\n" +
	"# [appearance.scrollbar]: style = thin, track; thumb/track = a one-cell glyph\n" +
	"#               (track also takes \"none\"); tint = border, muted, #RRGGBB\n" +
	"# background: off (your terminal shows through), theme (the theme's\n" +
	"#               background), or #RRGGBB, for every surface at once\n" +
	"# pane_background, desktop_background, window_chrome_background,\n" +
	"#               dock_background, [appearance.sidebar] background: the same\n" +
	"#               values for one surface; empty follows background, and a\n" +
	"#               colour a program or the chrome chose always wins\n" +
	"# ============================================================================\n\n" +

	"# ============================================================================\n" +
	"# KEYBINDINGS\n" +
	"# ============================================================================\n" +
	"# Set an action to [] to unbind it and hand the key back to the shell.\n" +
	"# An empty list and a missing line are not the same thing. A line this file\n" +
	"# does not have gets its default back the next time tuios starts; an action\n" +
	"# set to [] stays empty.\n" +
	"#\n" +
	"# You do not have to edit this by hand. In tuios, open the keybind manager\n" +
	"# (leader then k, or the command palette) and press ctrl+d on a binding to\n" +
	"# remove it, or ctrl+x to take its key off every action. From a shell:\n" +
	"#\n" +
	"#   tuios keybinds unbind close_window w   # one key off one action\n" +
	"#   tuios keybinds free alt+left           # off every action\n" +
	"#\n" +
	"# `tuios keybinds doctor` says which binding in each scope is live, what\n" +
	"# clashes with what, and which keys never reach the program in the pane.\n" +
	"#\n" +
	"# [keybindings.global] acts in window mode and terminal mode alike. It binds\n" +
	"# ctrl+p to the command palette and alt+space to the launcher, which costs\n" +
	"# you fish's history-back and readline's set-mark. To move or drop either:\n" +
	"#\n" +
	"#   [keybindings.global]\n" +
	"#   command_palette = [\"ctrl+shift+p\"]\n" +
	"#   launcher = []\n" +
	"#\n" +
	"# [keybindings.terminal_mode] binds alt+arrows to move focus between panes.\n" +
	"# In readline, fish and zsh, alt+left and alt+right move the cursor a word\n" +
	"# at a time. If you want those back:\n" +
	"#\n" +
	"#   [keybindings.terminal_mode]\n" +
	"#   terminal_focus_left = []\n" +
	"#   terminal_focus_right = []\n" +
	"#\n" +
	"# alt+up and alt+down are unclaimed by the common shells, so they are the\n" +
	"# safer pair to keep.\n" +
	"#\n" +
	"# shift+pgup and shift+pgdown scroll one page. middle pastes from the host\n" +
	"# clipboard. Write ctrl+middle to add a modifier.\n" +
	"#\n" +
	"# hold_window_mode binds a key that puts tuios in window-management mode for\n" +
	"# as long as it is physically held, and hands the previous mode back when it\n" +
	"# is let go. In window-management mode the key does nothing and stays a\n" +
	"# plain modifier, so alt chords there keep their own bindings:\n" +
	"#\n" +
	"#   [keybindings.mode_control]\n" +
	"#   hold_window_mode = [\"leftalt\"]\n" +
	"#\n" +
	"# It needs a terminal that speaks the Kitty keyboard protocol (Ghostty, kitty,\n" +
	"# WezTerm, foot, Alacritty; not Terminal.app), because nothing else reports\n" +
	"# that a key was released. Naming a modifier key (leftalt, rightalt, leftctrl,\n" +
	"# leftsuper) asks the terminal for one more thing on top: every keystroke in\n" +
	"# the session then arrives as an escape code. Any ordinary key (f13 to f63,\n" +
	"# scrolllock, a spare letter) avoids that. Unbound by default.\n" +
	"# ============================================================================\n\n" +

	"# ============================================================================\n" +
	"# DOCK COMPONENTS\n" +
	"# ============================================================================\n" +
	"# The dock is three ordered lists of component names. Leave [dock] out\n" +
	"# entirely and the bar draws what it always drew; write the lists to\n" +
	"# reorder it, and omit a name to drop that segment.\n" +
	"#\n" +
	"#   [dock]\n" +
	"#   left   = [\"mode\", \"workspaces\", \"trail\", \"tape\"]\n" +
	"#   center = [\"windows\"]\n" +
	"#   right  = [\"notifications\", \"copy-help\", \"cpu\", \"ram\", \"clock\", \"session-controls\"]\n" +
	"#\n" +
	"# A cell of your own is a command whose first line of stdout is the text:\n" +
	"#\n" +
	"#   [dock.custom.branch]\n" +
	"#   command = \"git branch --show-current\"\n" +
	"#   refresh = \"event:after-focus-change\"   # or once, push, or \"30s\"\n" +
	"#\n" +
	"# and then \"custom/branch\" in one of the lists above. A component that\n" +
	"# fails is hidden rather than left showing a stale value;\n" +
	"# `tuios list-dock-components` says which and why.\n" +
	"# ============================================================================\n\n"

func renderConfigFile(cfg *UserConfig, configPath string) ([]byte, error) {
	data, err := MarshalUserConfig(cfg)
	if err != nil {
		return nil, errfmt.Errorf("failed to marshal config: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(configFileHeader(configPath))
	if _, err := sb.Write(data); err != nil {
		return nil, errfmt.Errorf("failed to write config data: %w", err)
	}
	return []byte(sb.String()), nil
}

// MarshalUserConfig is cfg as TOML, the way every config file tuios writes is
// encoded. It differs from toml.Marshal in one thing: a field may write its own
// TOML, which is how max_fps comes out as a bare 144 or a quoted "auto" from
// one field. toml.Marshal would quote both.
func MarshalUserConfig(cfg *UserConfig) ([]byte, error) {
	cfg = cfg.withoutFoldedMenus()
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf).SetIndentSymbol("  ").EnableMarshalerInterface()
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeConfigBytes(data []byte, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return errfmt.Errorf("failed to create config directory: %w", err)
	}
	target := configPath
	if resolved, err := filepath.EvalSymlinks(configPath); err == nil {
		target = resolved
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	noteSelfWrite(data)
	if err := atomicfile.Write(target, data, mode, false); err != nil {
		return errfmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

var selfWrites struct {
	sync.Mutex
	entries [8]selfWrite
	seq     uint64
}

type selfWrite struct {
	sum [sha256.Size]byte
	seq uint64
}

func noteSelfWrite(data []byte) {
	sum := sha256.Sum256(data)
	selfWrites.Lock()
	selfWrites.seq++
	selfWrites.entries[selfWrites.seq%uint64(len(selfWrites.entries))] = selfWrite{sum: sum, seq: selfWrites.seq}
	selfWrites.Unlock()
}

func lastSelfWrite() uint64 {
	selfWrites.Lock()
	defer selfWrites.Unlock()
	return selfWrites.seq
}

func selfWriteAfter(sum [sha256.Size]byte, after uint64) uint64 {
	selfWrites.Lock()
	defer selfWrites.Unlock()
	var found uint64
	for _, e := range selfWrites.entries {
		if e.seq > after && e.seq > found && e.sum == sum {
			found = e.seq
		}
	}
	return found
}

// WriteConfigFile marshals cfg to TOML (with the documented header) and writes
// it to configPath, creating the parent directory as needed.
func WriteConfigFile(cfg *UserConfig, configPath string) error {
	data, err := renderConfigFile(cfg, configPath)
	if err != nil {
		return err
	}
	return writeConfigBytes(data, configPath)
}

var (
	saveMu  sync.Mutex
	saveSeq atomic.Uint64
	keyGen  = map[string]uint64{}
)

// RenderUserConfig reads cfg into the change a save makes and hands back the
// function that writes it. The split exists because the caller is the Update
// goroutine: reading the model is memory and can happen there, the file write
// cannot.
//
// Reading cfg here rather than in the returned function is also what makes this
// safe without a deep copy. The config is the model's own and goes on being
// edited; a writer holding the pointer would be marshalling a struct changing
// underneath it.
//
// The change is the difference between the config cfg was loaded from and cfg
// now, so a save writes the keys the person changed and nothing else (see
// include_write.go). The next save starts from here, so a change is written
// once.
//
// The returned function is safe to call from anywhere and from several places at
// once. Writes are serialised and stamped, so when two saves are in flight the
// older one gives way rather than overwriting the newer. Its WriteNote says
// when a read-only file sent a change to another file.
func RenderUserConfig(cfg *UserConfig) (func() (WriteNote, error), error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, errfmt.Errorf("failed to resolve config path: %w", err)
	}
	data, err := MarshalUserConfig(cfg)
	if err != nil {
		return nil, errfmt.Errorf("failed to marshal config: %w", err)
	}
	base := cfg.baseline
	cfg.baseline = data
	keys := saveKeys{held: cfg.heldPaths(), forced: cfg.takeForced()}
	gen := saveSeq.Add(1)
	return func() (WriteNote, error) {
		saveMu.Lock()
		defer saveMu.Unlock()
		note, err := saveConfigData(configPath, base, data, gen, keys)
		if err != nil {
			return note, &SaveError{Err: err, cfg: cfg, base: base, forced: keys.forced}
		}
		return note, nil
	}, nil
}

// SaveError is a save that failed. The change it carried is not in any file,
// so RewindSave puts the config's baseline back and the next save writes the
// change again.
type SaveError struct {
	Err    error
	cfg    *UserConfig
	base   []byte
	forced [][]string
}

func (e *SaveError) Error() string { return e.Err.Error() }
func (e *SaveError) Unwrap() error { return e.Err }

// RewindSave undoes what RenderUserConfig did to cfg's baseline when the save
// err came from failed. Call it on the goroutine that owns cfg. It does
// nothing for another config or another error.
func RewindSave(cfg *UserConfig, err error) {
	var se *SaveError
	if cfg == nil || !errors.As(err, &se) || se.cfg != cfg {
		return
	}
	cfg.baseline = se.base
	cfg.restoreForced(se.forced)
}

// SaveUserConfig persists cfg to the user's config file at the standard XDG
// location, the same way the settings page does.
func SaveUserConfig(cfg *UserConfig) (WriteNote, error) {
	write, err := RenderUserConfig(cfg)
	if err != nil {
		return WriteNote{}, err
	}
	note, err := write()
	RewindSave(cfg, err)
	return note, err
}
