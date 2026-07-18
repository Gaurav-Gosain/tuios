package config

import (
	"strings"
	"testing"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// keySeeds are key spellings the normalizer has to survive: the ordinary
// chords, the multi-byte AZERTY letters and shifted spellings it recently
// gained, and the malformed shapes a hand-edited config produces.
var keySeeds = []string{
	"",
	" ",
	"\t\n ",
	"a", "A", "m", "M", "z",
	"é", "É", "è", "à", "ç", "ß", "İ", "ı",
	"世", "👍",
	"ctrl+a", "Ctrl+A", "CTRL+A",
	"alt+1", "opt+1", "option+1",
	"shift+1", "shift+a", "shift+A", "shift+é",
	"ctrl+shift+alt+x",
	"opt+shift+3",
	"tab", "shift+tab", "opt+tab",
	"enter", "esc", "escape", "space",
	"f1", "f12", "f99",
	"up", "down", "left", "right",
	// Malformed.
	"+", "++", "+++", "shift+", "+a", "ctrl+",
	"shift+shift+shift+a",
	strings.Repeat("ctrl+", 512) + "a",
	strings.Repeat("+", 4096),
	strings.Repeat("a", 4096),
	// Invalid UTF-8: a config file is bytes and TOML does not guarantee the
	// key survived as valid UTF-8 through every path.
	"\xff", "\xff\xfe", "shift+\xff", "\xed\xa0\x80",
	"\xc3", "a\xc3",
	// Separator and case interactions.
	"SHIFT+\xff",
	"Shift+É",
	"shift+0", "shift+9", "shift+-", "shift+=",
	"!", "@", "#", "$", "%", "^", "&", "*", "(", ")",
}

// FuzzNormalizeKey checks the key normalizer's invariants. It runs on strings
// pulled straight from a TOML config, so it must not panic on a malformed or
// non-UTF-8 spelling, and its output has to stay usable as a keybinding table
// key: no duplicates, no empty entries where the input was not empty, and a
// bounded number of aliases.
func FuzzNormalizeKey(f *testing.F) {
	for _, s := range keySeeds {
		f.Add(s)
	}

	kn := NewKeyNormalizer()

	f.Fuzz(func(t *testing.T, key string) {
		if len(key) > 1<<12 {
			key = key[:1<<12]
		}

		got := kn.NormalizeKey(key)

		// The normalizer produces the key itself plus a fixed, small set of
		// platform aliases. An input-proportional result would mean it is
		// expanding rather than normalizing.
		const maxAliases = 8
		if len(got) > maxAliases {
			t.Fatalf("NormalizeKey(%q) returned %d aliases: %q", key, len(got), got)
		}

		seen := make(map[string]bool, len(got))
		for _, k := range got {
			if seen[k] {
				t.Fatalf("NormalizeKey(%q) returned duplicate %q in %q", key, k, got)
			}
			seen[k] = true
			// An alias is never longer than the trimmed key plus the longest
			// prefix the normalizer prepends ("option+"). The factor of three
			// covers case folding: strings.ToLower rewrites each byte of an
			// invalid UTF-8 sequence as a three-byte U+FFFD.
			if len(k) > 3*len(strings.TrimSpace(key))+len("option+") {
				t.Fatalf("NormalizeKey(%q) produced oversized alias %q", key, k)
			}
		}

		// A non-blank key must normalize to something usable, or the binding
		// silently disappears from the table.
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			if len(got) == 0 {
				t.Fatalf("NormalizeKey(%q) returned no aliases", key)
			}
			if got[0] == "" {
				t.Fatalf("NormalizeKey(%q) returned an empty primary alias", key)
			}
		}

		// Normalizing is idempotent: feeding the primary alias back in must
		// keep producing that same primary alias, or a config reload would
		// drift the binding table.
		if len(got) > 0 && got[0] != "" {
			again := kn.NormalizeKey(got[0])
			if len(again) == 0 || again[0] != got[0] {
				t.Fatalf("NormalizeKey not idempotent for %q: %q then %q", key, got, again)
			}
		}

		// ValidateKey shares the same string handling and must not panic or
		// return an empty reason alongside a failure.
		if ok, reason := kn.ValidateKey(key); !ok && reason == "" {
			t.Fatalf("ValidateKey(%q) rejected the key with no reason", key)
		}
	})
}

// FuzzExpandKeys drives the multi-key path, where the deduplication runs across
// keys rather than within one.
func FuzzExpandKeys(f *testing.F) {
	f.Add("ctrl+a\nctrl+b\nshift+1")
	f.Add("a\nA\na\nA")
	f.Add("\n\n\n")
	f.Add("é\nÉ\nshift+é")
	f.Add(strings.Repeat("ctrl+a\n", 256))
	f.Add("\xff\n\xfe\n")

	kn := NewKeyNormalizer()

	f.Fuzz(func(t *testing.T, joined string) {
		if len(joined) > 1<<12 {
			joined = joined[:1<<12]
		}
		keys := strings.Split(joined, "\n")

		got := kn.ExpandKeys(keys)

		seen := make(map[string]bool, len(got))
		for _, k := range got {
			if seen[k] {
				t.Fatalf("ExpandKeys(%q) returned duplicate %q", keys, k)
			}
			seen[k] = true
		}
		// Each key contributes a bounded number of aliases, so the expansion
		// stays proportional to the input rather than exploding.
		if len(got) > len(keys)*8 {
			t.Fatalf("ExpandKeys expanded %d keys into %d entries", len(keys), len(got))
		}
	})
}

// FuzzLoadConfigPipeline drives the parse-and-fill pipeline that LoadUserConfig
// runs on a config file's bytes, without touching the filesystem. Everything
// after toml.Unmarshal operates on attacker-shaped values (negative sizes, huge
// counts, unknown enum strings, arbitrary keybinding tables), and the fill
// stage is what is supposed to sanitise them before the rest of the app reads
// the config.
func FuzzLoadConfigPipeline(f *testing.F) {
	seeds := []string{
		"",
		"[appearance]\nborder_style = \"rounded\"\n",
		"[appearance]\nscrollback_lines = -1\nscroll_lines = -1\n",
		"[appearance]\nscrollback_lines = 9223372036854775807\n",
		"[appearance]\nscroll_lines = 9223372036854775807\n",
		"[appearance]\nborder_style = \"\"\ndockbar_position = \"\"\n",
		"[appearance]\nborder_style = \"nonsense\"\nwhichkey_position = \"nonsense\"\n",
		"[appearance]\nwindow_title_position = \"\xff\"\n",
		"[appearance]\ntitle_format = \"{{{{{{\"\n",
		"[appearance]\nanimations_enabled = false\nconfirm_quit = true\n",
		"[daemon]\nlog_level = \"trace\"\ndefault_codec = \"json\"\n",
		"[daemon]\nlog_level = \"nonsense\"\ndefault_codec = \"nonsense\"\n",
		"[daemon]\nsocket_path = \"/dev/null\"\n",
		"[keybindings]\nleader_key = \"\"\n",
		"[keybindings]\nleader_key = \"ctrl+a\"\n",
		"[keybindings]\nleader_key = \"\xff\"\n",
		"[keybindings.terminal_mode]\nquit = [\"ctrl+q\", \"ctrl+q\"]\n",
		"[keybindings.terminal_mode]\nquit = []\n",
		"[keybindings.workspace]\nnext = [\"\", \"\", \"\"]\n",
		"[keybindings.layout]\ntile = [\"" + strings.Repeat("a", 4096) + "\"]\n",
		"[hooks]\n",
		"not toml at all",
		"[[[[",
		strings.Repeat("[a]\n", 4096),
		strings.Repeat("x = 1\n", 4096),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 1<<16 {
			src = src[:1<<16]
		}

		var cfg UserConfig
		if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
			// Malformed TOML is reported, not parsed. That is the contract.
			return
		}

		done := make(chan *ValidationResult, 1)
		go func() {
			defaultCfg := DefaultConfig()
			fillMissingAppearance(&cfg, defaultCfg)
			fillMissingDaemon(&cfg, defaultCfg)
			fillMissingKeybinds(&cfg, defaultCfg)
			done <- ValidateConfig(&cfg)
		}()

		var result *ValidationResult
		select {
		case result = <-done:
		case <-time.After(30 * time.Second):
			t.Fatalf("config pipeline did not terminate for %d bytes", len(src))
		}

		// The fill stage exists so the rest of the app can read these without
		// re-checking. A value that survives it out of range is a real defect:
		// scrollback sizes the ring buffer and scroll_lines drives a loop.
		if cfg.Appearance.ScrollbackLines < 100 || cfg.Appearance.ScrollbackLines > 1000000 {
			t.Fatalf("scrollback_lines survived fill as %d", cfg.Appearance.ScrollbackLines)
		}
		if cfg.Appearance.ScrollLines < 1 || cfg.Appearance.ScrollLines > 50 {
			t.Fatalf("scroll_lines survived fill as %d", cfg.Appearance.ScrollLines)
		}
		// Fill must never leave an enum blank; a blank one reaches the renderer
		// as an unknown style.
		if cfg.Appearance.BorderStyle == "" {
			t.Fatalf("border_style survived fill as empty")
		}
		if cfg.Appearance.DockbarPosition == "" {
			t.Fatalf("dockbar_position survived fill as empty")
		}
		if cfg.Daemon.DefaultCodec == "" {
			t.Fatalf("default_codec survived fill as empty")
		}

		// Every validation finding must be reportable to the user.
		for _, e := range result.Errors {
			if e.Message == "" {
				t.Fatalf("validation error on %s/%s has no message", e.Field, e.Key)
			}
		}
		for _, w := range result.Warnings {
			if w.Message == "" {
				t.Fatalf("validation warning on %s/%s has no message", w.Field, w.Key)
			}
		}

		// Validating twice must agree: a second load of the same file cannot
		// suddenly start rejecting it.
		second := ValidateConfig(&cfg)
		if len(second.Errors) != len(result.Errors) {
			t.Fatalf("re-validating changed the error count: %d then %d",
				len(result.Errors), len(second.Errors))
		}
	})
}
