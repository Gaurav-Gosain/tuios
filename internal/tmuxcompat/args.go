package tmuxcompat

import (
	"fmt"
	"strings"
)

// Global holds the options tmux takes before the command.
type Global struct {
	// Socket is -S, a server socket path.
	Socket string
	// Name is -L, a server socket name.
	Name string
	// Version is -V.
	Version bool
	// Ignored lists the global flags accepted and ignored: -2, -u, -v, -N and
	// -f with its file.
	Ignored []string
}

// ParseGlobal reads the global options at the front of a tmux argv (argv[0]
// excluded) and returns them with the command words that follow.
func ParseGlobal(args []string) (Global, []string, error) {
	var g Global
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		// Clustered flags: -2u, -uS path, -Spath.
		j := 1
		for j < len(a) {
			c := a[j]
			switch c {
			case 'S', 'L', 'f', 'c', 'T':
				val := a[j+1:]
				if val == "" {
					if i+1 >= len(args) {
						return g, nil, fmt.Errorf("option requires an argument: -%c", c)
					}
					i++
					val = args[i]
				}
				switch c {
				case 'S':
					g.Socket = val
				case 'L':
					g.Name = val
				case 'f':
					g.Ignored = append(g.Ignored, "-f")
				case 'c':
					// -c shell-command runs a command in the default shell,
					// the way sh -c does. It is not a tmux command.
					return g, nil, fmt.Errorf("-c is not supported by the tuios tmux shim")
				case 'T':
					g.Ignored = append(g.Ignored, "-T")
				}
				j = len(a)
			case 'V':
				g.Version = true
				j++
			case '2', 'u', 'v', 'N', 'l', 'D':
				g.Ignored = append(g.Ignored, "-"+string(c))
				j++
			case 'C':
				return g, nil, fmt.Errorf("control mode (-C) is not supported by the tuios tmux shim")
			default:
				return g, nil, fmt.Errorf("unknown option: -%c", c)
			}
		}
		i++
	}
	return g, args[i:], nil
}

// SplitCommands splits command words into the commands separated by a ";"
// argument, the way tmux reads `a \; b`. A word ending in an unescaped ";"
// also ends its command.
func SplitCommands(words []string) [][]string {
	var out [][]string
	var cur []string
	for _, w := range words {
		switch {
		case w == ";":
			if len(cur) > 0 {
				out = append(out, cur)
			}
			cur = nil
		case strings.HasSuffix(w, "\\;"):
			cur = append(cur, strings.TrimSuffix(w, "\\;")+";")
		case strings.HasSuffix(w, ";") && len(w) > 1:
			cur = append(cur, strings.TrimSuffix(w, ";"))
			out = append(out, cur)
			cur = nil
		default:
			cur = append(cur, w)
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// spec is the flags one command accepts: bools take no value, values take one.
type spec struct {
	bools  string
	values string
}

// Parsed is one command's flags and positional arguments.
type Parsed struct {
	flags map[byte]bool
	vals  map[byte][]string
	// Args are the positional arguments, after the flags or after "--".
	Args []string
}

// Has reports whether bool flag c was given.
func (p Parsed) Has(c byte) bool { return p.flags[c] }

// Value returns the last value given for flag c, and whether it was given.
func (p Parsed) Value(c byte) (string, bool) {
	v := p.vals[c]
	if len(v) == 0 {
		return "", false
	}
	return v[len(v)-1], true
}

// Values returns every value given for flag c, in order.
func (p Parsed) Values(c byte) []string { return p.vals[c] }

// errUnknownFlag is a flag the command's spec does not name.
type errUnknownFlag struct {
	cmd  string
	flag byte
}

func (e errUnknownFlag) Error() string {
	return fmt.Sprintf("%s: unknown flag -%c", e.cmd, e.flag)
}

// parseFlags reads a command's argv (the command name excluded) the way tmux's
// getopt does: flags first, clustered or not, a value attached or in the next
// word, "--" ending them, and the first word that is not a flag starting the
// positional arguments.
func parseFlags(cmd string, sp spec, args []string) (Parsed, error) {
	p := Parsed{flags: map[byte]bool{}, vals: map[byte][]string{}}
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		j := 1
		for j < len(a) {
			c := a[j]
			switch {
			case strings.IndexByte(sp.bools, c) >= 0:
				p.flags[c] = true
				j++
			case strings.IndexByte(sp.values, c) >= 0:
				val := a[j+1:]
				if val == "" {
					if i+1 >= len(args) {
						return p, fmt.Errorf("%s: -%c needs a value", cmd, c)
					}
					i++
					val = args[i]
				}
				p.vals[c] = append(p.vals[c], val)
				j = len(a)
			default:
				return p, errUnknownFlag{cmd: cmd, flag: c}
			}
		}
		i++
	}
	p.Args = append([]string(nil), args[i:]...)
	return p, nil
}
