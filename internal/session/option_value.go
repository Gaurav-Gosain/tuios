package session

import (
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	optionSourceSession = "session"
	optionSourceConfig  = "config"
	optionSourceDefault = "default"
)

type sessionOptions struct {
	sess *Session
	file *config.UserConfig
	lc   *config.LayeredConfig
}

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

const optionTheme = "appearance.theme"

func followOmarchy(name string) string {
	if name == "" && theme.OmarchyInstalled() {
		return theme.OmarchyID
	}
	return name
}

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

var emptyUserConfig = sync.OnceValue(func() *config.UserConfig {
	cfg, err := config.ParseUserConfig(nil)
	if err != nil {
		return nil
	}
	return cfg
})

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
