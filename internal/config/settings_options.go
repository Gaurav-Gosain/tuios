package config

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

type settingsOption struct {
	index int
	opt   Option
	keep  bool
}

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
