package session

import (
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Option sources, as get-option reports them.
const (
	optionSourceSession = "session"
	optionSourceConfig  = "config"
	optionSourceDefault = "default"
)

// sessionOptions answers what an option is for one session, in the order the
// client applies them: the session's override, then config.toml and the files
// it brings in, then the built-in default.
//
// The middle step is why this exists. The client reads config.toml itself and
// draws by it, but the daemon held only the overrides set-option writes. Every
// reader here skipped the file, so with appearance.background = "theme" in
// config.toml get-config said off while the panes were painted.
//
// The files are read once, when the reader is made, so a request that asks for
// several options sees one version of them. A file that cannot be read or does
// not parse counts as setting nothing.
type sessionOptions struct {
	sess *Session
	file *config.UserConfig
	lc   *config.LayeredConfig
}

// optionsFor makes the reader for sess, which may be nil, on behalf of the
// caller on cs, which may be nil for the daemon itself.
//
// A caller that came over a link reads no config file. get-option needs only
// the list capability on a link, and config.toml holds command strings that
// can carry tokens (notifications.agent.command,
// appearance.selection.copy_command, launcher.gui_command). Such a caller gets
// what it got before the file was read at all: the session's override, then
// the default.
func (d *Daemon) optionsFor(cs *connState, sess *Session) sessionOptions {
	o := sessionOptions{sess: sess}
	if d.configPath == "" || (cs != nil && cs.viaLink) {
		return o
	}
	lc, err := config.LoadLayered(d.configPath)
	if err != nil {
		return o
	}
	data, err := lc.Bytes()
	if err != nil {
		return o
	}
	uc, err := config.ParseUserConfig(data)
	if err != nil {
		return o
	}
	o.file, o.lc = uc, lc
	return o
}

// lookup returns the value in effect for path and where it came from. A path
// the registry does not carry and the session never set returns ok false.
func (o sessionOptions) lookup(path string) (value, source string, ok bool) {
	value, source, ok = o.lookupRaw(path)
	if path == optionTheme {
		value = followOmarchy(value)
	}
	return value, source, ok
}

func (o sessionOptions) lookupRaw(path string) (value, source string, ok bool) {
	if o.sess != nil {
		if v, set := o.sess.GetOption(path); set {
			return v, optionSourceSession, true
		}
	}
	opt, known := config.LookupOption(path)
	if !known {
		return "", "", false
	}
	if v, set := o.fromFile(path); set {
		return v, optionSourceConfig, true
	}
	return config.EffectiveDefault(opt), optionSourceDefault, true
}

// optionTheme is the theme option, whose empty value means something.
const optionTheme = "appearance.theme"

// followOmarchy is the theme an empty appearance.theme puts on screen: the
// client follows Omarchy where it is installed, so no theme reads as omarchy
// there. Anywhere else, and for any other name, the name stands.
func followOmarchy(name string) string {
	if name == "" && theme.OmarchyInstalled() {
		return theme.OmarchyID
	}
	return name
}

// fromFile is the value the config files give path, when one of them sets it.
// A key the files leave out holds whatever the fill left, which for some keys
// is not the default, so it counts only when it differs from what an empty
// file parses to. That difference is a legacy key the parse folded in, such as
// appearance.sidebar_position into appearance.sidebar.position, which the
// client draws by although no file names the new path.
func (o sessionOptions) fromFile(path string) (string, bool) {
	if o.file == nil {
		return "", false
	}
	v, ok := config.GetOptionValue(o.file, path)
	if !ok {
		return "", false
	}
	if _, set := o.lc.Origin(strings.Split(path, ".")); set {
		return v, true
	}
	if empty := emptyUserConfig(); empty != nil {
		if ev, eok := config.GetOptionValue(empty, path); eok && ev != v {
			return v, true
		}
	}
	return "", false
}

// emptyUserConfig is what a config file with nothing in it parses to.
var emptyUserConfig = sync.OnceValue(func() *config.UserConfig {
	cfg, err := config.ParseUserConfig(nil)
	if err != nil {
		return nil
	}
	return cfg
})

// get is the value in effect for path, or empty for an unknown path.
func (o sessionOptions) get(path string) string {
	v, _, _ := o.lookup(path)
	return v
}

func (o sessionOptions) getInt(path string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(o.get(path)))
	return n, err == nil
}

func (o sessionOptions) getBool(path string) (bool, bool) {
	switch o.get(path) {
	case "true", "on", "1", "yes", "enabled":
		return true, true
	case "false", "off", "0", "no", "disabled":
		return false, true
	}
	return false, false
}
