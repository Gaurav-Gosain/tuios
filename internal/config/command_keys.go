package config

import (
	"fmt"
	"strings"
	"unicode"
)

// Command keybindings: [[keybindings.command]] entries that bind a key to a
// command the user writes, after herdr's custom command keybindings.
//
//	[[keybindings.command]]
//	key = "prefix+alt+g"
//	type = "scratch"
//	command = "lazygit"
//	description = "lazygit"
//
// The key is written as elsewhere in the config, with one addition: a key
// that starts with "prefix+" acts after the leader (the [keybindings.prefix_mode]
// scope), and any other key acts in window mode and terminal mode alike (the
// [keybindings.global] scope). The entries are not written into those
// sections. The registry reads them beside the sections, so the config file
// keeps them in one table and a save never copies them elsewhere.
//
// An entry is the action "command:<name>". The name is the entry's own name
// field, or else a slug of its description, or else of its command. A
// scratch entry keeps its pane under that name, so the name has to stay the
// same from one run to the next, and the user can pin it.

// Command types.
const (
	CommandTypeScratch = "scratch"
	CommandTypePopup   = "popup"
	CommandTypePane    = "pane"
	CommandTypeShell   = "shell"
)

// CommandActionPrefix starts the action name of every command entry.
const CommandActionPrefix = "command:"

// DefaultScratchName is the name of the built-in scratch terminal, the one
// toggle_scratch shows. A command entry cannot take it.
const DefaultScratchName = "scratch"

// CommandBinding is one [[keybindings.command]] entry.
type CommandBinding struct {
	// Key is the key that runs the command. "prefix+" puts it after the
	// leader. Any other key acts in window mode and terminal mode.
	Key string `toml:"key"`
	// Type is scratch, popup, pane or shell. Empty means popup.
	Type string `toml:"type,omitempty"`
	// Command is run by sh -c, so it can hold pipes and quotes. A scratch
	// entry with no command runs the user's shell.
	Command string `toml:"command,omitempty"`
	// Description names the entry in the command palette and in
	// tuios keybinds list. Optional.
	Description string `toml:"description,omitempty"`
	// Name is the entry's stable name. Optional: see ResolvedName.
	Name string `toml:"name,omitempty"`
	// Width and Height size a scratch or popup entry, in cells (60) or
	// percent (80%) of the pane region. Empty means 80%.
	Width  string `toml:"width,omitempty"`
	Height string `toml:"height,omitempty"`
}

// ResolvedType is the entry's type with the default filled in.
func (c CommandBinding) ResolvedType() string {
	if t := strings.ToLower(strings.TrimSpace(c.Type)); t != "" {
		return t
	}
	return CommandTypePopup
}

// ResolvedName is the entry's name: its own name field, else a slug of its
// description, else a slug of its command.
func (c CommandBinding) ResolvedName() string {
	for _, s := range []string{c.Name, c.Description, c.Command} {
		if slug := commandSlug(s); slug != "" {
			return slug
		}
	}
	return ""
}

// Action is the entry's action name, for the registry and the dispatcher.
func (c CommandBinding) Action() string {
	return CommandActionPrefix + c.ResolvedName()
}

// Label is what the palette and keybinds list call the entry.
func (c CommandBinding) Label() string {
	if d := strings.TrimSpace(c.Description); d != "" {
		return d
	}
	if cmd := strings.TrimSpace(c.Command); cmd != "" {
		return "Run " + cmd
	}
	return c.ResolvedName()
}

// WidthSpec and HeightSpec are the effective sizes. A size that does not
// parse falls back to 80%, as a popup does.
func (c CommandBinding) WidthSpec() string  { return scratchSpec(c.Width, ScratchDefaultWidth) }
func (c CommandBinding) HeightSpec() string { return scratchSpec(c.Height, ScratchDefaultHeight) }

// Section and BareKey split the key into the section it acts in and the key
// as that section writes it: "prefix+alt+g" is alt+g in prefix_mode, and
// "alt+g" is alt+g in global.
func (c CommandBinding) Section() string {
	if _, ok := cutPrefixKey(c.Key); ok {
		return SectionPrefixMode
	}
	return SectionGlobal
}

// BareKey is the key without "prefix+".
func (c CommandBinding) BareKey() string {
	if rest, ok := cutPrefixKey(c.Key); ok {
		return rest
	}
	return strings.TrimSpace(c.Key)
}

func cutPrefixKey(key string) (string, bool) {
	key = strings.TrimSpace(key)
	if len(key) > len("prefix+") && strings.EqualFold(key[:len("prefix+")], "prefix+") {
		return key[len("prefix+"):], true
	}
	return "", false
}

// commandSlug lowercases s and keeps letters, digits and single dashes.
func commandSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// commandProblem says what is wrong with an entry, or "".
func commandProblem(c CommandBinding, normalizer *KeyNormalizer) string {
	switch t := c.ResolvedType(); {
	case strings.TrimSpace(c.Key) == "":
		return "The entry has no key. Add a key, for example key = \"prefix+alt+g\"."
	case t != CommandTypeScratch && t != CommandTypePopup && t != CommandTypePane && t != CommandTypeShell:
		return fmt.Sprintf("The type %q is not known. Use scratch, popup, pane or shell.", c.Type)
	case strings.TrimSpace(c.Command) == "" && t != CommandTypeScratch:
		return "The entry has no command. Add a command."
	case c.ResolvedName() == "":
		return "The entry has no name tuios can use. Add a name with letters or digits."
	case c.ResolvedName() == DefaultScratchName:
		return "The name scratch belongs to the built-in scratch terminal. Use a different name."
	}
	if ok, msg := normalizer.ValidateKey(c.BareKey()); !ok {
		return msg
	}
	for _, f := range []struct{ key, spec string }{{"width", c.Width}, {"height", c.Height}} {
		if strings.TrimSpace(f.spec) == "" {
			continue
		}
		if _, _, err := ParseBoxSize(f.spec); err != nil {
			return fmt.Sprintf("The %s is not valid: %v.", f.key, err)
		}
	}
	return ""
}

// Commands returns the entries tuios uses: every valid entry, with a second
// entry of the same name left out. validateCommands warns about the others.
func (k *KeybindingsConfig) Commands() []CommandBinding {
	if len(k.Command) == 0 {
		return nil
	}
	normalizer := NewKeyNormalizer()
	seen := map[string]bool{}
	out := make([]CommandBinding, 0, len(k.Command))
	for _, c := range k.Command {
		if commandProblem(c, normalizer) != "" || seen[c.ResolvedName()] {
			continue
		}
		seen[c.ResolvedName()] = true
		out = append(out, c)
	}
	return out
}

// CommandFor returns the entry behind an action name, if the action is one.
func (k *KeybindingsConfig) CommandFor(action string) (CommandBinding, bool) {
	name, ok := strings.CutPrefix(action, CommandActionPrefix)
	if !ok {
		return CommandBinding{}, false
	}
	for _, c := range k.Commands() {
		if c.ResolvedName() == name {
			return c, true
		}
	}
	return CommandBinding{}, false
}

// commandSection is the key lists the command entries add to one section, by
// action name.
func (k *KeybindingsConfig) commandSection(section string) map[string][]string {
	var out map[string][]string
	for _, c := range k.Commands() {
		if c.Section() != section {
			continue
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[c.Action()] = []string{c.BareKey()}
	}
	return out
}

// validateCommands warns about each entry tuios leaves out. An entry is a
// warning and not an error, so a mistake in one entry never stops tuios.
func validateCommands(cfg *UserConfig, result *ValidationResult) {
	normalizer := NewKeyNormalizer()
	seen := map[string]bool{}
	for i, c := range cfg.Keybindings.Command {
		field := fmt.Sprintf("keybindings.command[%d]", i+1)
		if msg := commandProblem(c, normalizer); msg != "" {
			result.Warnings = append(result.Warnings, ValidationError{Field: field, Key: c.Key, Message: msg + " tuios ignores this entry."})
			continue
		}
		if seen[c.ResolvedName()] {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: field, Key: c.Key,
				Message: fmt.Sprintf("An earlier entry has the name %q. Add a different name. tuios ignores this entry.", c.ResolvedName()),
			})
			continue
		}
		seen[c.ResolvedName()] = true
	}
}
