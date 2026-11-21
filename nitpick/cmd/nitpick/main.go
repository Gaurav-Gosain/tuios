// Command nitpick runs the nitpick rules on Go packages. It can limit the
// report to the lines that a change touched.
//
// Usage:
//
//	nitpick check [flags] [packages]
//	nitpick diff [flags] [packages]
//	nitpick rules [--json]
//
// Run nitpick help for the flags.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"tuios.dev/nitpick"
)

const usage = `nitpick finds Go hygiene problems: dropped errors, development defaults,
comments that say what the code says, and deep nesting.

Usage:
  nitpick check [flags] [packages]   Report the findings in the packages (default ./...).
  nitpick diff [flags] [packages]    Report the findings on changed lines only.
  nitpick rules [--json]             List the rules and their precision.

Flags:
  --base REF      Compare with REF (diff only). The default is HEAD.
                  For a REF other than HEAD, diff starts at the merge base.
  --staged        Use the staged changes only (diff only).
  --enable LIST   Turn on more rules. LIST is names with commas, or all.
  --disable LIST  Turn off rules. LIST is names with commas.
  --tests=false   Do not check _test.go files.
  --json          Write JSON for tools and agents.
  --gate P        Exit with 1 when a finding has a precision of P or more.
                  P is a fraction (0.9) or a percent (90%).

Without packages, diff checks the packages of the changed files.

Exit status: 0 when the gate passes, 1 when it fails, 2 on an error.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type options struct {
	base, enable, disable string
	staged, json, tests   bool
	gate                  float64
	gateSet               bool
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "check", "diff", "rules":
	default:
		fmt.Fprintf(stderr, "nitpick: %q is not a command. Run nitpick help to see the commands.\n", cmd)
		return 2
	}

	var o options
	fs := flag.NewFlagSet("nitpick "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.json, "json", false, "")
	if cmd != "rules" {
		fs.StringVar(&o.base, "base", "HEAD", "")
		fs.BoolVar(&o.staged, "staged", false, "")
		fs.StringVar(&o.enable, "enable", "", "")
		fs.StringVar(&o.disable, "disable", "", "")
		fs.BoolVar(&o.tests, "tests", true, "")
		fs.Func("gate", "", func(s string) error {
			v, err := parseGate(s)
			o.gate, o.gateSet = v, true
			return err
		})
	}
	patterns, err := parseArgs(fs, args)
	if err != nil {
		fmt.Fprintf(stderr, "nitpick: %v\n", err)
		return 2
	}
	if cmd == "rules" {
		return listRules(stdout, o.json)
	}
	if cmd == "check" && (o.base != "HEAD" || o.staged) {
		fmt.Fprintln(stderr, "nitpick: --base and --staged work with nitpick diff only.")
		return 2
	}
	rules, err := selectRules(o.enable, o.disable)
	if err != nil {
		fmt.Fprintf(stderr, "nitpick: %v\n", err)
		return 2
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "nitpick: %v\n", err)
		return 2
	}
	var scope changes
	if cmd == "diff" {
		scope, err = gitChanges(cwd, o.base, o.staged)
		if err != nil {
			fmt.Fprintf(stderr, "nitpick: %v\n", err)
			return 2
		}
		if len(patterns) == 0 {
			patterns = scope.packages(cwd)
		}
	} else if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	var res result
	if len(patterns) > 0 {
		res = analyze(cwd, patterns, rules, o.tests)
	}
	if scope != nil {
		res.Findings = slices.DeleteFunc(res.Findings, func(f finding) bool { return !scope.has(f.path, f.Line) })
	}
	if res.Findings == nil {
		res.Findings = []finding{}
	}
	for i := range res.Findings {
		res.Findings[i].File = relPath(cwd, res.Findings[i].path)
		if o.gateSet && res.Findings[i].Precision >= o.gate {
			res.GateFailed = true
		}
	}
	if o.gateSet {
		res.Gate = &o.gate
	}

	if o.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(stderr, "nitpick: %v\n", err)
			return 2
		}
	} else {
		writeText(stdout, stderr, res, cmd)
	}
	switch {
	case len(res.Errors) > 0:
		return 2
	case res.GateFailed:
		return 1
	}
	return 0
}

func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return rest, nil
		}
		if args[0] == "--" {
			return append(rest, args[1:]...), nil
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
}

func parseGate(s string) (float64, error) {
	pct := strings.HasSuffix(s, "%")
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil {
		return 0, fmt.Errorf("the gate %q is not a number", s)
	}
	if pct || v > 1 {
		v /= 100
	}
	if v < 0 || v > 1 {
		return 0, fmt.Errorf("the gate %q is not from 0 to 1 or from 0%% to 100%%", s)
	}
	return v, nil
}

func selectRules(enable, disable string) ([]nitpick.Rule, error) {
	on := map[string]bool{}
	for _, r := range nitpick.Rules {
		on[r.Name()] = r.Default
	}
	set := func(list string, v bool) error {
		for name := range strings.SplitSeq(list, ",") {
			name = strings.TrimSpace(name)
			switch {
			case name == "":
			case name == "all":
				for k := range on {
					on[k] = v
				}
			default:
				if _, ok := nitpick.Lookup(name); !ok {
					return fmt.Errorf("%q is not a rule. Run nitpick rules to see the rules", name)
				}
				on[name] = v
			}
		}
		return nil
	}
	if err := set(enable, true); err != nil {
		return nil, err
	}
	if err := set(disable, false); err != nil {
		return nil, err
	}
	var out []nitpick.Rule
	for _, r := range nitpick.Rules {
		if on[r.Name()] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no rule is on")
	}
	return out, nil
}

func listRules(w io.Writer, asJSON bool) int {
	type row struct {
		Name      string  `json:"name"`
		Default   bool    `json:"default"`
		Precision float64 `json:"precision"`
		Sample    int     `json:"sample"`
		Doc       string  `json:"doc"`
	}
	var rows []row
	for _, r := range nitpick.Rules {
		doc, _, _ := strings.Cut(r.Analyzer.Doc, "\n")
		rows = append(rows, row{r.Name(), r.Default, r.Precision, r.Sample, doc})
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return 2
		}
		return 0
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tDEFAULT\tPRECISION\tRULE TEXT")
	for _, r := range rows {
		state := "off"
		if r.Default {
			state = "on"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, state, percent(r.Precision), r.Doc)
	}
	if err := tw.Flush(); err != nil {
		return 2
	}
	return 0
}

func percent(p float64) string {
	return strconv.Itoa(int(p*100+0.5)) + "%"
}

func writeText(stdout, stderr io.Writer, res result, cmd string) {
	for _, f := range res.Findings {
		fmt.Fprintf(stdout, "%s:%d:%d: %s: %s (precision %s)\n", f.File, f.Line, f.Column, f.Rule, f.Message, percent(f.Precision))
	}
	for _, e := range res.Errors {
		fmt.Fprintf(stderr, "nitpick: %s\n", e)
	}
	where := ""
	if cmd == "diff" {
		where = " on changed lines"
	}
	switch n := len(res.Findings); n {
	case 0:
		fmt.Fprintf(stderr, "nitpick: no findings%s.\n", where)
	case 1:
		fmt.Fprintf(stderr, "nitpick: 1 finding%s.\n", where)
	default:
		fmt.Fprintf(stderr, "nitpick: %d findings%s.\n", n, where)
	}
	if res.GateFailed {
		fmt.Fprintf(stderr, "nitpick: the gate failed. A finding has a precision of %s or more.\n", percent(*res.Gate))
	}
}
