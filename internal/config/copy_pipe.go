package config

import (
	"fmt"
	"strings"
)

// Copy-pipe keybindings: [[keybindings.copy_pipe]] entries that bind a key in
// copy mode to a yank piped through a command, after tmux's copy-pipe and
// copy-pipe-and-cancel.
//
//	[[keybindings.copy_pipe]]
//	key = "p"
//	command = "tr '\n' ' '"
//	cancel = true
//	description = "flatten"
//
// They are a table of their own and not a type of [[keybindings.command]]: a
// command entry's key acts in window mode, terminal mode or after the leader
// and shows in the palette, while a copy-pipe key acts only in copy mode, on
// the selection, and is usually a bare letter. Sharing the table would have
// meant a special case in the key scopes, the palette, the doctor and the
// bare-letter warning for every copy-pipe entry.

// CopyPipeBinding is one [[keybindings.copy_pipe]] entry.
type CopyPipeBinding struct {
	// Key is the copy-mode key, written as elsewhere in the config.
	Key string `toml:"key"`
	// Command is run by sh -c with the selection on stdin. What it writes to
	// stdout goes to the clipboard.
	Command string `toml:"command"`
	// Cancel leaves copy mode after the yank (copy-pipe-and-cancel). False
	// stays in copy mode at the same place (copy-pipe).
	Cancel bool `toml:"cancel,omitempty"`
	// Description names the entry in the dock messages. Optional.
	Description string `toml:"description,omitempty"`
}

// Label is what the dock calls the entry: its description, else its command.
func (c CopyPipeBinding) Label() string {
	if d := strings.TrimSpace(c.Description); d != "" {
		return d
	}
	return CopyCommandLabel(c.Command)
}

// CopyCommandLabel is a command line short enough for a dock message.
func CopyCommandLabel(command string) string {
	command = strings.Join(strings.Fields(command), " ")
	if r := []rune(command); len(r) > 32 {
		command = string(r[:31]) + "…"
	}
	return command
}

// copyPipeProblem says what is wrong with an entry, or "".
func copyPipeProblem(c CopyPipeBinding, normalizer *KeyNormalizer) string {
	if strings.TrimSpace(c.Key) == "" {
		return "The entry has no key. Add a key, for example key = \"p\"."
	}
	if strings.TrimSpace(c.Command) == "" {
		return "The entry has no command. Add a command."
	}
	if ok, msg := normalizer.ValidateKey(strings.TrimSpace(c.Key)); !ok {
		return msg
	}
	return ""
}

// CopyPipes returns the entries tuios uses: every valid entry, with a second
// entry on the same key left out. validateCopyPipes warns about the others.
func (k *KeybindingsConfig) CopyPipes() []CopyPipeBinding {
	if len(k.CopyPipe) == 0 {
		return nil
	}
	normalizer := NewKeyNormalizer()
	seen := map[string]bool{}
	var out []CopyPipeBinding
	for _, c := range k.CopyPipe {
		key := CanonicalKey(c.Key)
		if copyPipeProblem(c, normalizer) != "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// CopyPipeFor returns the entry on a copy-mode key, if there is one.
func (k *KeybindingsConfig) CopyPipeFor(key string) (CopyPipeBinding, bool) {
	key = CanonicalKey(key)
	for _, c := range k.CopyPipes() {
		if CanonicalKey(c.Key) == key {
			return c, true
		}
	}
	return CopyPipeBinding{}, false
}

// validateCopyPipes warns about each entry tuios leaves out.
func validateCopyPipes(cfg *UserConfig, result *ValidationResult) {
	normalizer := NewKeyNormalizer()
	seen := map[string]bool{}
	for i, c := range cfg.Keybindings.CopyPipe {
		field := fmt.Sprintf("keybindings.copy_pipe[%d]", i+1)
		if msg := copyPipeProblem(c, normalizer); msg != "" {
			result.Warnings = append(result.Warnings, ValidationError{Field: field, Key: c.Key, Message: msg + " tuios ignores this entry."})
			continue
		}
		key := CanonicalKey(c.Key)
		if seen[key] {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: field, Key: c.Key,
				Message: fmt.Sprintf("An earlier entry has the key %s. Use a different key. tuios ignores this entry.", c.Key),
			})
			continue
		}
		seen[key] = true
	}
}
