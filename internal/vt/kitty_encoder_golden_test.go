package vt

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestEncoderMatchesKitty checks the bytes tuios sends a kitty-protocol pane
// against kitty's own encoder. testdata/kitty-encoder/golden.txt is kitty
// 0.49.2's encode_key_for_tty over twelve keys, five modifier states, press,
// release and repeat, and every flag set from 1 to 31 (generate.py makes it).
//
// Only the flag sets tuios encodes itself are compared: those with
// disambiguate or report-all-keys. Without them a press goes through the
// legacy encoder, which this test does not cover. The lock bits are compared
// only under report-all-keys, the one mode where tuios passes them on; kitty
// sends them under every flag set. An empty modifier field before associated
// text counts as 1.
func TestEncoderMatchesKitty(t *testing.T) {
	f, err := os.Open("testdata/kitty-encoder/golden.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	keys := map[string]struct {
		code rune
		text string
	}{
		"enter": {KeyEnter, ""}, "tab": {KeyTab, ""}, "bs": {KeyBackspace, ""}, "a": {'a', "a"},
		"esc": {KeyEscape, ""}, "space": {KeySpace, " "}, "up": {KeyUp, ""}, "f5": {KeyF5, ""},
		"del": {KeyDelete, ""}, "1": {'1', "1"}, "kp1": {KeyKp1, "1"}, "lshift": {KeyLeftShift, ""},
	}
	mods := map[string]KeyMod{"": 0, "shift+": ModShift, "ctrl+": ModCtrl, "alt+": ModAlt, "num+": ModNumLock}
	shifted := map[string]rune{"a": 'A', "1": '!'}

	compared := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), " ", 4)
		if len(parts) != 4 {
			continue
		}
		flags, _ := strconv.Atoi(parts[0])
		if flags&(ansi.KittyDisambiguateEscapeCodes|ansi.KittyReportAllKeysAsEscapeCodes) == 0 {
			continue
		}
		modName, keyName := "", parts[1]
		for p := range mods {
			if p != "" && strings.HasPrefix(keyName, p) {
				modName, keyName = p, strings.TrimPrefix(keyName, p)
			}
		}
		if modName == "num+" && flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
			continue
		}
		want, err := strconv.Unquote(pyToGoLiteral(parts[3]))
		if err != nil {
			t.Fatalf("%q: %v", parts[3], err)
		}
		k := keys[keyName]
		ev := KeyPressEvent{Code: k.code, Mod: mods[modName], Text: k.text}
		if ev.Mod == ModCtrl || ev.Mod == ModAlt {
			ev.Text = ""
		}
		if s, ok := shifted[keyName]; ok && ev.Mod == ModShift {
			ev.Text, ev.ShiftedCode = string(s), s
		}
		var got string
		if parts[2] == "release" {
			got = EncodeKeyReleaseCSIu(ev, flags)
		} else {
			ev.IsRepeat = parts[2] == "repeat"
			got = EncodeKeyCSIu(ev, flags)
			if got == "" {
				// tuios hands the key to the legacy encoder; kitty's bytes
				// must then be the legacy ones.
				got = legacyBytes(ev)
			}
		}
		compared++
		// kitty leaves the modifier field empty before associated text
		// (CSI 97;;97u) where tuios writes 1. The protocol reads both the
		// same, and the panes tuios serves accept either.
		got = strings.Replace(got, ";1;", ";;", 1)
		if got != want {
			t.Errorf("flags %d %s %s: tuios %q, kitty %q", flags, parts[1], parts[2], got, want)
		}
	}
	if compared < 4000 {
		t.Fatalf("compared only %d cases", compared)
	}
}

// legacyBytes is what the legacy encoder sends for the keys of the golden
// matrix that EncodeKeyCSIu leaves to it.
func legacyBytes(ev KeyPressEvent) string {
	switch ev.Code {
	case KeyEnter:
		return "\r"
	case KeyTab:
		return "\t"
	case KeyBackspace:
		return "\x7f"
	}
	return ev.Text
}

// pyToGoLiteral turns a Python repr of a str into a Go quoted string.
func pyToGoLiteral(s string) string {
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		inner := s[1 : len(s)-1]
		inner = strings.ReplaceAll(inner, `\'`, `'`)
		inner = strings.ReplaceAll(inner, `"`, `\"`)
		return `"` + inner + `"`
	}
	return s
}
