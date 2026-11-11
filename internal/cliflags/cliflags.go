// Package cliflags holds the interface flags every binary that renders the
// TUI registers: `tuios` and its TUI commands, `tuios ssh`, and `tuios-web`.
//
// They live here so the binaries cannot drift apart and each one accepts every
// override on the command line. The package is kept apart from internal/config so the
// widely imported config package does not pick up a pflag dependency.
package cliflags

import (
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/pflag"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Interface is the values of the interface flags. The zero value applies
// nothing, which is what a flag left unset means.
//
// Most interface flags set a config option for this run, and come from the
// option registry: an option whose entry names a Flag gets that flag, with
// the option's type, description and check. The fields below are the flags
// that are not options.
type Interface struct {
	ASCIIOnly bool
	ThemeName string
	// ShowKeys turns the key display overlay on. It is a session option
	// rather than an appearance override, so Overrides leaves it out.
	ShowKeys     bool
	NoAnimations bool

	// sets are the option flags given, in the order they were parsed.
	sets []config.OptionSet
}

// Register adds the interface flags to fs, binding them to i.
func (i *Interface) Register(fs *pflag.FlagSet) {
	fs.BoolVar(&i.ASCIIOnly, "ascii-only", false, "Use ASCII characters instead of Nerd Font icons")
	fs.StringVar(&i.ThemeName, "theme", "", "Color theme to use (e.g., dracula, nord, tokyonight). Leave empty to use standard terminal colors without theming")
	fs.BoolVar(&i.ShowKeys, "show-keys", false, "Enable showkeys overlay to display pressed keys")
	fs.BoolVar(&i.NoAnimations, "no-animations", false, "Disable UI animations for instant transitions")
	for _, opt := range optionFlags() {
		f := fs.VarPF(&optionFlag{opt: opt, sets: &i.sets}, opt.Flag, "", "")
		if opt.Type == config.OptionBool {
			f.NoOptDefVal = "true"
		}
	}
}

// Overrides is the flags as the config overrides they layer over the file.
func (i *Interface) Overrides() config.Overrides {
	return config.Overrides{
		Sets:         i.sets,
		ASCIIOnly:    i.ASCIIOnly,
		NoAnimations: i.NoAnimations,
		ThemeName:    i.ThemeName,
	}
}

// optionFlagUsage is an option flag's help line: the option's description,
// the values it takes, what it sets, and its default. It is built only when
// help is shown (see optionFlag.HelpUsage): the default comes from the
// registry's derived defaults, which a normal run of tuios never needs.
func optionFlagUsage(opt config.Option) string {
	values := ""
	switch {
	case len(opt.Accepted) > 0:
		values = "one of " + strings.Join(opt.Accepted, ", ") + "; "
	case opt.Type == config.OptionInt && opt.Max > 0:
		values = fmt.Sprintf("from %d to %d; ", opt.Min, opt.Max)
	}
	usage := fmt.Sprintf("%s (%ssets %s for this run; default: from config, else %s",
		strings.TrimSuffix(opt.Description, "."), values, opt.Path, opt.Default)
	if opt.Deprecated != "" {
		usage += "; deprecated: " + opt.Deprecated
	}
	return usage + ")"
}

// optionFlags are the options with a flag, read once: tuios registers the
// flags on several commands at every start.
var optionFlags = sync.OnceValue(config.OptionFlags)

// optionFlag is a flag that sets one config option for this run.
type optionFlag struct {
	opt   config.Option
	sets  *[]config.OptionSet
	value string
}

func (f *optionFlag) String() string { return f.value }

func (f *optionFlag) Type() string { return f.opt.Type }

// HelpUsage is the flag's help line, built when help is shown. See
// fang.FlagUsage.
func (f *optionFlag) HelpUsage() string {
	opt, _ := config.LookupOption(f.opt.Path)
	return optionFlagUsage(opt)
}

// Set checks value as set-config does, on a scratch config, and records it.
// A value the option does not take fails here, so the command line reports
// it before anything runs.
func (f *optionFlag) Set(value string) error {
	if err := config.SetOptionValue(config.DefaultConfig(), f.opt.Path, value); err != nil {
		return err
	}
	f.value = value
	*f.sets = append(*f.sets, config.OptionSet{Path: f.opt.Path, Value: value})
	return nil
}
