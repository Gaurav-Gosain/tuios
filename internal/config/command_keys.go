package config

import (
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"
	"unicode/utf8"
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
//
// A slug keeps ASCII letters and digits only, so a description in another
// script can give an empty one. The key comes next, which is ASCII. The last
// resort is a hash of the entry's text, which is the same on every run.
func (c CommandBinding) ResolvedName() string {
	for _, s := range []string{c.Name, c.Description, c.Command, c.Key} {
		if slug := commandSlug(s); slug != "" {
			return slug
		}
	}
	text := c.Name + "\x00" + c.Description + "\x00" + c.Command + "\x00" + c.Key
	if strings.Trim(text, "\x00") == "" {
		return ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(text))
	return fmt.Sprintf("cmd-%08x", h.Sum32())
}

// nameSource is the field ResolvedName made the name from, and its text:
// "name", "description", "command" or "key", or "" for a hash or no name.
func (c CommandBinding) nameSource() (field, text string) {
	for _, f := range []struct{ field, text string }{
		{"name", c.Name}, {"description", c.Description}, {"command", c.Command}, {"key", c.Key},
	} {
		if commandSlug(f.text) != "" {
			return f.field, f.text
		}
	}
	return "", ""
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

// commandSlugMax is the longest name tuios makes from a description or a
// command. Two texts that agree up to here get the same name.
const commandSlugMax = 40

// commandSlug lowercases s and keeps letters, digits and single dashes.
func commandSlug(s string) string { return commandSlugN(s, commandSlugMax) }

// commandSlugN is commandSlug cut at max bytes, or not cut when max is 0.
func commandSlugN(s string, max int) string {
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
		if max > 0 && b.Len() >= max {
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

// isLeader reports whether key is the leader key. The leader is read before
// any section, so an entry on it never runs.
func (k *KeybindingsConfig) isLeader(key string) bool {
	leader := k.LeaderKey
	if leader == "" {
		leader = DefaultLeaderKey
	}
	return CanonicalKey(key) == CanonicalKey(leader)
}

// LeaderAction is what Bindings names as the taker of a command entry's key
// when the key is the leader.
const LeaderAction = "leader_key"

// CommandProblem is a [[keybindings.command]] entry that needs attention:
// one tuios ignores, or one whose key every pane loses.
type CommandProblem struct {
	// Entry is the entry's place in config.toml, from 1.
	Entry int    `json:"entry"`
	Key   string `json:"key"`
	// Name is the name tuios gives the entry, when it can make one.
	Name string `json:"name,omitempty"`
	// Ignored is true when tuios leaves the entry out.
	Ignored bool   `json:"ignored"`
	Problem string `json:"problem"`
}

// CommandProblems lists what is wrong with the command entries, in file
// order. validateCommands warns with it at load, and the doctor prints it.
func (k *KeybindingsConfig) CommandProblems() []CommandProblem {
	normalizer := NewKeyNormalizer()
	first := map[string]int{}
	var out []CommandProblem
	for i, c := range k.Command {
		entry := i + 1
		name := c.ResolvedName()
		if msg := commandProblem(c, normalizer); msg != "" {
			out = append(out, CommandProblem{Entry: entry, Key: c.Key, Name: name, Ignored: true, Problem: msg + " tuios ignores this entry."})
			continue
		}
		if prev, ok := first[name]; ok {
			out = append(out, CommandProblem{Entry: entry, Key: c.Key, Name: name, Ignored: true, Problem: nameClash(c, prev, name)})
			continue
		}
		first[name] = entry
		if c.Section() == SectionGlobal && bareLetterKey(c.BareKey()) {
			out = append(out, CommandProblem{Entry: entry, Key: c.Key, Name: name,
				Problem: fmt.Sprintf("The key %s has no modifier and no prefix+, so tuios takes it from every pane. Use prefix+%s or add a modifier, for example alt+%s.", c.BareKey(), c.BareKey(), c.BareKey()),
			})
		}
	}
	return out
}

// nameClash says why an entry lost its name to entry prev. An entry with no
// name of its own gets one made from its text, and two different texts can
// make the same name: tuios keeps only the first 40 characters, and drops
// case and punctuation. That case says which text the name came from, since
// nothing in the file shows the name.
func nameClash(c CommandBinding, prev int, name string) string {
	field, text := c.nameSource()
	if field == "name" || field == "" {
		return fmt.Sprintf("An earlier entry has the name %q. Add a different name. tuios ignores this entry.", name)
	}
	cut := ""
	if len(commandSlugN(text, 0)) > commandSlugMax {
		cut = fmt.Sprintf(" tuios uses only the first %d characters.", commandSlugMax)
	}
	// The suggestion is itself a name, so it has to fit in the same 40
	// characters or it would be cut back to the clash.
	suggest := strings.TrimRight(name[:min(len(name), commandSlugMax-2)], "-") + "-2"
	return fmt.Sprintf("This entry has no name, so tuios makes the name %q from its %s.%s Entry %d has the same name. Add a name to this entry, for example name = %q. tuios ignores this entry.",
		name, field, cut, prev, suggest)
}

// validateCommands warns about each entry tuios leaves out. An entry is a
// warning and not an error, so a mistake in one entry never stops tuios.
func validateCommands(cfg *UserConfig, result *ValidationResult) {
	for _, p := range cfg.Keybindings.CommandProblems() {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: fmt.Sprintf("keybindings.command[%d]", p.Entry), Key: p.Key, Message: p.Problem,
		})
	}
}

// bareLetterKey reports whether key is one printable character with no
// modifier: a key a person types into a pane.
func bareLetterKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "space" {
		return true
	}
	r, size := utf8.DecodeRuneInString(key)
	return size == len(key) && size > 0 && unicode.IsPrint(r)
}
