package config

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// A Settings field that holds one option's value as it is, a bool or a
// string, names the option in an opt tag, such as opt:"appearance.show_clock".
// applyOptionFields fills every such field from the config, so the field and
// its option cannot drift, and a field needs no copy line of its own in
// ApplyAppearanceConfig. Fields the config does not hold as they are (a
// clamped number, a parsed format, a value two options decide) stay
// hand-written there.

// settingsOption is one tagged Settings field and its option. keep, from
// opt:"path,keep", leaves the field as it is when the file does not set the
// option (a nil pointer, an empty string), so a value the session holds, the
// built-in default or one a run set, stands, as it did when each field was
// copied by hand.
type settingsOption struct {
	index int
	opt   Option
	keep  bool
}

// settingsOptions are the tagged fields of Settings, read once. A tag that
// names no option, or an option whose type the field cannot hold, panics:
// every test that resolves Settings runs this.
var settingsOptions = sync.OnceValue(func() []settingsOption {
	t := reflect.TypeFor[Settings]()
	var out []settingsOption
	for i := range t.NumField() {
		tag, ok := t.Field(i).Tag.Lookup("opt")
		if !ok {
			continue
		}
		path, flag, _ := strings.Cut(tag, ",")
		opt, found := LookupOption(path)
		if !found {
			panic(errfmt.Errorf("config: Settings.%s: opt tag %q names no option", t.Field(i).Name, path))
		}
		want := map[string]reflect.Kind{OptionBool: reflect.Bool, OptionString: reflect.String, OptionInt: reflect.Int}[opt.Type]
		if t.Field(i).Type.Kind() != want {
			panic(errfmt.Errorf("config: Settings.%s: option %s is %s, the field is %s", t.Field(i).Name, path, opt.Type, t.Field(i).Type.Kind()))
		}
		out = append(out, settingsOption{index: i, opt: opt, keep: flag == "keep"})
	}
	return out
})

// applyOptionFields sets every tagged Settings field from cfg. An option the
// file does not set takes its default, or, for a keep field, leaves the field
// as it is. A closed-set option holding a value outside its set takes its
// default: ValidateConfig warns about it. A colour option's keywords are not a
// closed set, since it takes a literal as well.
func applyOptionFields(cfg *UserConfig, s *Settings) {
	v := reflect.ValueOf(s).Elem()
	for _, f := range settingsOptions() {
		if f.keep && optionUnset(cfg, f.opt) {
			continue
		}
		text, _ := GetOptionValue(cfg, f.opt.Path)
		switch f.opt.Type {
		case OptionBool:
			b, err := parseOptionBool(text)
			if err != nil {
				b, _ = parseOptionBool(f.opt.Default)
			}
			v.Field(f.index).SetBool(b)
		case OptionString:
			if len(f.opt.Accepted) > 0 && !f.opt.Color {
				if f.opt.Normalize != nil {
					text = f.opt.Normalize(text)
				}
				if !slices.Contains(f.opt.Accepted, text) {
					text = f.opt.Default
				}
			}
			v.Field(f.index).SetString(text)
		case OptionInt:
			v.Field(f.index).SetInt(int64(f.opt.resolveInt(text)))
		}
	}
}

// resolveInt is an int option's value as the session uses it: 0 under
// ZeroIsDefault, and anything that is not a number, reads as the default, and
// a value outside Min to Max is moved to the nearer end. ValidateConfig warns
// about both.
func (o Option) resolveInt(text string) int {
	n, err := strconv.Atoi(text)
	if err != nil || (n == 0 && o.Zero == ZeroIsDefault) {
		n, _ = strconv.Atoi(o.Default)
	}
	if o.Max > 0 {
		n = min(max(n, o.Min), o.Max)
	}
	return n
}

// optionUnset reports whether cfg leaves o unset: a nil pointer, an empty
// string, or 0 for an int option whose 0 means unset.
func optionUnset(cfg *UserConfig, o Option) bool {
	field, ok := resolveOptionField(cfg, o.Path)
	if !ok {
		return true
	}
	if field.Kind() == reflect.Pointer {
		return field.IsNil()
	}
	switch field.Kind() {
	case reflect.String:
		return field.String() == ""
	case reflect.Int:
		return field.Int() == 0 && o.Zero == ZeroIsDefault
	}
	return false
}
