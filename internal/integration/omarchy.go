package integration

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// The Omarchy integration: the tuios agents widget in the Omarchy bar, and a
// tuios row in the Omarchy menu. Omarchy 4 keeps its plugins in
// ~/.config/omarchy/plugins/ID and the menu rows of the person in
// ~/.config/omarchy/extensions/omarchy-menu.jsonc. Omarchy reads both from
// the home folder whatever XDG_CONFIG_HOME says, so tuios does too.
//
// integrations/omarchy is the source. assets/omarchy is a copy for the
// archive, and TestOmarchyPluginMatchesItsSource keeps the two the same.

// OmarchyID is the name integration install, status and uninstall take for
// Omarchy.
const OmarchyID = "omarchy"

// OmarchyPluginID is the id of the bar widget plugin.
const OmarchyPluginID = "tuios.agents"

const (
	omarchyAssets = "assets/omarchy/tuios.agents/"
	// omarchyMenuScript is the menu, a bash script with jq, as Omarchy's own
	// menus are.
	omarchyMenuScript = "tuios-menu"
	// omarchyWidgetMark is in the Widget.qml tuios ships, so a plugin folder
	// tuios wrote is told from one of the person's own.
	omarchyWidgetMark = "// tuios agents in the Omarchy bar."
	// omarchyMenuMarker is the whole-line comment above the menu row install
	// adds. The Omarchy menu strips only whole-line comments, so the marker
	// cannot sit at the end of the row.
	omarchyMenuMarker = "// tuios integration: the next line is the tuios row"
	omarchyMenuGlyph  = "\uf120"
)

// OmarchyRunner runs a program found on PATH, omarchy-shell or omarchy, and
// returns its output. A nil runner means omarchy-shell is not there.
type OmarchyRunner func(name string, args ...string) (string, error)

// OmarchyDir is the Omarchy config folder.
func OmarchyDir(env Env) string { return filepath.Join(env.Home, ".config", "omarchy") }

// OmarchyPluginDir is the folder of the bar widget plugin.
func OmarchyPluginDir(env Env) string {
	return filepath.Join(OmarchyDir(env), "plugins", OmarchyPluginID)
}

// OmarchyMenuPath is the person's menu extension file.
func OmarchyMenuPath(env Env) string {
	return filepath.Join(OmarchyDir(env), "extensions", "omarchy-menu.jsonc")
}

// omarchyFiles are the files of the plugin folder as this build ships them
// for the tuios binary at the path tuios: the widget's default command and
// menu, and the menu's tuios line, are filled in.
func omarchyFiles(env Env, tuios string) map[string][]byte {
	a := assets()
	word := func(s string) []byte { b, _ := jsonx.Marshal(s); return b }
	manifest := bytes.Replace(a[omarchyAssets+"manifest.json"], []byte(`"command": "tuios"`), append([]byte(`"command": `), word(tuios)...), 1)
	manifest = bytes.Replace(manifest, []byte(`"menu": "tuios-menu"`), append([]byte(`"menu": `), word(omarchyMenuPath(env))...), 1)
	return map[string][]byte{
		"manifest.json":   manifest,
		"Widget.qml":      a[omarchyAssets+"Widget.qml"],
		omarchyMenuScript: bytes.Replace(a[omarchyAssets+omarchyMenuScript], []byte("\ntuios=tuios\n"), []byte("\ntuios="+shellWord(tuios)+"\n"), 1),
	}
}

// omarchyMenuPath is the menu script in the plugin folder.
func omarchyMenuPath(env Env) string { return filepath.Join(OmarchyPluginDir(env), omarchyMenuScript) }

// omarchyMenuRow is the row install adds to the menu file.
func omarchyMenuRow(env Env) string {
	b, _ := jsonx.Marshal(struct {
		Icon        string `json:"icon"`
		Label       string `json:"label"`
		Description string `json:"description"`
		Action      string `json:"action"`
	}{omarchyMenuGlyph, "tuios", "Agents that need you, the inbox and your sessions", shellWord(omarchyMenuPath(env))})
	return `"tuios": ` + string(b) + ","
}

// OmarchyStatus is what integration status says about Omarchy.
type OmarchyStatus struct {
	Dir, Plugin, Menu string
	// Installed says Omarchy's config folder is there.
	Installed bool
	// PluginState is missing, current, older or foreign.
	PluginState string
	// MenuRow is marked (install's row), by_hand (a tuios row of the
	// person's own) or missing.
	MenuRow string
}

// OmarchyResult is what install or uninstall did, as lines a person reads.
type OmarchyResult struct {
	Notes []string
}

// omarchyPluginState sorts the plugin folder against the files this build
// ships for tuios.
func omarchyPluginState(env Env, tuios string) (string, error) {
	dir := OmarchyPluginDir(env)
	if _, err := os.Stat(dir); err != nil {
		return "missing", nil
	}
	state := "current"
	for name, want := range omarchyFiles(env, tuios) {
		have, err := readOptional(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		if name == "Widget.qml" && !bytes.Contains(have, []byte(omarchyWidgetMark)) {
			return "foreign", nil
		}
		if !bytes.Equal(have, want) {
			state = "older"
		}
	}
	return state, nil
}

// omarchyMenuState sorts the menu file's tuios row.
func omarchyMenuState(data []byte) string {
	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		if strings.TrimSpace(l) == omarchyMenuMarker {
			return "marked"
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), `"tuios":`) {
			return "by_hand"
		}
	}
	return "missing"
}

// withoutOmarchyRow is the menu file with the marked row and its marker
// taken out.
func withoutOmarchyRow(data []byte) []byte {
	if data == nil {
		return nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	var kept []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == omarchyMenuMarker {
			if i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), `"tuios":`) {
				i++
			}
			continue
		}
		kept = append(kept, lines[i])
	}
	return []byte(strings.Join(kept, ""))
}

// withOmarchyRow is the menu file with the marked row put first in its
// object, right after the opening brace. The Omarchy menu takes a trailing
// comma, so no line of the person's own changes, and uninstall gives the
// file back byte for byte. A file whose first line that is not a comment
// does not open an object is refused: the Omarchy menu drops every row of a
// file it cannot read.
func withOmarchyRow(data []byte, env Env) ([]byte, error) {
	row := "  " + omarchyMenuMarker + "\n  " + omarchyMenuRow(env) + "\n"
	if strings.TrimSpace(string(data)) == "" {
		return []byte("{\n" + row + "}\n"), nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		if t != "{" {
			return nil, errors.New("its first line that is not a comment is not a lone {")
		}
		if !strings.HasSuffix(l, "\n") {
			lines[i] = l + "\n"
		}
		out := strings.Join(lines[:i+1], "") + row + strings.Join(lines[i+1:], "")
		return []byte(out), nil
	}
	return nil, errors.New("it holds no JSON object")
}

// OmarchyInstall writes the plugin folder, adds the menu row, and has the
// shell load and turn on the widget. force replaces a plugin folder that is
// not one tuios wrote.
func OmarchyInstall(env Env, tuios string, run OmarchyRunner, force bool) (OmarchyResult, error) {
	var res OmarchyResult
	if fi, err := os.Stat(OmarchyDir(env)); err != nil || !fi.IsDir() {
		return res, errfmt.Errorf("omarchy is not set up here: %s is missing. Install Omarchy 4, then run this again", OmarchyDir(env))
	}
	state, err := omarchyPluginState(env, tuios)
	if err != nil {
		return res, err
	}
	dir := OmarchyPluginDir(env)
	switch state {
	case "foreign":
		if !force {
			return res, errfmt.Errorf("%s holds a plugin tuios did not write, so it was left as it is. Run with --force to replace it", dir)
		}
		fallthrough
	case "missing", "older":
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return res, errfmt.Errorf("failed to make %s: %w", dir, err)
		}
		for name, data := range omarchyFiles(env, tuios) {
			path := filepath.Join(dir, name)
			have, err := readOptional(path)
			if err != nil {
				return res, err
			}
			if err := writeAtomic(path, have, data); err != nil {
				return res, err
			}
		}
		if err := os.Chmod(omarchyMenuPath(env), 0o755); err != nil { //nolint:gosec // the menu script runs
			return res, err
		}
		res.Notes = append(res.Notes, "Wrote the tuios agents widget to "+dir+".")
	case "current":
		res.Notes = append(res.Notes, "The tuios agents widget in "+dir+" is current.")
	}

	menu := OmarchyMenuPath(env)
	have, err := readOptional(menu)
	if err != nil {
		return res, err
	}
	if omarchyMenuState(have) == "by_hand" {
		res.Notes = append(res.Notes, menu+" already has a tuios row of your own, so it was left as it is.")
	} else {
		out, err := withOmarchyRow(withoutOmarchyRow(have), env)
		if err != nil {
			return res, errfmt.Errorf("%s was left as it is: %w", menu, err)
		}
		if !bytes.Equal(out, have) {
			if err := os.MkdirAll(filepath.Dir(menu), 0o750); err != nil {
				return res, errfmt.Errorf("failed to make %s: %w", filepath.Dir(menu), err)
			}
			if err := writeAtomic(menu, have, out); err != nil {
				return res, err
			}
			res.Notes = append(res.Notes, "Added the tuios row to "+menu+". The Omarchy menu reads the file again by itself.")
		}
	}

	note := "Turned on the tuios agents widget in the Omarchy bar."
	if run == nil {
		err = errors.New("omarchy-shell is not on PATH")
	} else if _, err = run("omarchy-shell", "shell", "rescanPlugins"); err == nil {
		_, err = run("omarchy", "plugin", "enable", OmarchyPluginID, "--yes")
	}
	if err != nil {
		note = "The widget is not on yet" + rexErr(err) + ". Run omarchy-shell shell rescanPlugins, then omarchy plugin enable " + OmarchyPluginID + "."
	}
	res.Notes = append(res.Notes, note)
	return res, nil
}

// OmarchyUninstall turns the widget off, removes the plugin folder tuios
// wrote and the marked menu row. A plugin or row of the person's own stays.
func OmarchyUninstall(env Env, tuios string, run OmarchyRunner) (OmarchyResult, error) {
	var res OmarchyResult
	state, err := omarchyPluginState(env, tuios)
	if err != nil {
		return res, err
	}
	switch state {
	case "current", "older":
		if run != nil {
			if _, err := run("omarchy", "plugin", "disable", OmarchyPluginID, "--yes"); err != nil {
				res.Notes = append(res.Notes, "Omarchy did not turn off the widget"+rexErr(err)+".")
			}
		}
		if err := os.RemoveAll(OmarchyPluginDir(env)); err != nil {
			return res, errfmt.Errorf("failed to remove %s: %w", OmarchyPluginDir(env), err)
		}
		res.Notes = append(res.Notes, "Removed "+OmarchyPluginDir(env)+".")
		if run != nil {
			_, _ = run("omarchy-shell", "shell", "rescanPlugins")
		}
	case "foreign":
		res.Notes = append(res.Notes, OmarchyPluginDir(env)+" holds a plugin tuios did not write, so it was left in place.")
	}
	menu := OmarchyMenuPath(env)
	have, err := readOptional(menu)
	if err != nil {
		return res, err
	}
	if out := withoutOmarchyRow(have); have != nil && !bytes.Equal(out, have) {
		if err := writeAtomic(menu, have, out); err != nil {
			return res, err
		}
		res.Notes = append(res.Notes, "Removed the tuios row from "+menu+".")
	}
	return res, nil
}

// OmarchyStatusOf reports the plugin folder and the menu row.
func OmarchyStatusOf(env Env, tuios string) (OmarchyStatus, error) {
	st := OmarchyStatus{Dir: OmarchyDir(env), Plugin: OmarchyPluginDir(env), Menu: OmarchyMenuPath(env)}
	if fi, err := os.Stat(st.Dir); err == nil && fi.IsDir() {
		st.Installed = true
	}
	state, err := omarchyPluginState(env, tuios)
	if err != nil {
		return st, err
	}
	st.PluginState = state
	have, err := readOptional(st.Menu)
	if err != nil {
		return st, err
	}
	st.MenuRow = omarchyMenuState(have)
	return st, nil
}
