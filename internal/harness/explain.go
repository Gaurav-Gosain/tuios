package harness

import (
	"fmt"
	"strings"
)

// DetectReport says what one manifest made of a process, and when it refused,
// what it was comparing against.
//
// It exists because detection was unfalsifiable from outside. A pane either was
// or was not an agent, with nothing said about which of comm, argv0, argv_path or
// exe_glob decided it, and no way to see what the daemon had actually read. That
// is how a registry where every exe_glob matched nothing shipped, and how a rule
// that matched any process with an agent's name in an argument went unnoticed
// until users reported unrelated panes turning into agents.
type DetectReport struct {
	ID      string `json:"id"`
	Matched bool   `json:"matched"`
	// Rule is the predicate that fired, as it reads in the manifest.
	Rule string `json:"rule,omitempty"`
	// Reason is why the manifest refused, naming the predicates it compared.
	Reason string `json:"reason,omitempty"`
}

// ProcReport is what the detector read about a process, before any manifest saw
// it. It is the other half of the answer: a rule that looks right and does not
// fire is usually reading something other than what its author assumed.
type ProcReport struct {
	Comm  string   `json:"comm"`
	Argv  []string `json:"argv"`
	Exe   string   `json:"exe"`
	Argv0 string   `json:"argv0"`
	// RunToken is the token an interpreter was asked to run, empty for a process
	// that is not an interpreter. It is the only argv any manifest may read.
	RunToken string `json:"run_token"`
	// Interpreter is why RunToken is or is not populated.
	Interpreter bool `json:"interpreter"`
}

// Describe reduces a process to what the matchers see.
func Describe(p ProcInfo) ProcReport {
	run := p.RunToken()
	commBase, exeBase := p.CommBase(), p.ExeBase()
	return ProcReport{
		Comm:        p.Comm,
		Argv:        p.Argv,
		Exe:         p.Exe,
		Argv0:       p.Argv0Base(),
		RunToken:    run,
		Interpreter: (commBase == "" || IsInterpreter(commBase)) || (exeBase != "" && IsInterpreter(exeBase)),
	}
}

// ExplainDetect runs every manifest against a process and reports what each one
// made of it, in the order Identify consults them. The first matched entry is the
// one Identify returns.
func (r *Registry) ExplainDetect(p ProcInfo) []DetectReport {
	run := p.RunToken()
	out := make([]DetectReport, 0, len(r.manifests))
	for _, m := range r.manifests {
		rep := DetectReport{ID: m.ID}
		if rule, ok := m.Detect.matches(p, run); ok {
			rep.Matched, rep.Rule = true, rule
		} else {
			rep.Reason = m.Detect.refusal(p, run)
		}
		out = append(out, rep)
	}
	return out
}

// refusal says why a manifest did not match, naming what it compared against so
// the answer can be checked without reading the matcher's source.
func (d *Detect) refusal(p ProcInfo, run string) string {
	var parts []string
	if d.Require.any() && !d.Require.satisfied(p) {
		where := "exe " + quoteOrNone(p.Exe)
		if p.Exe == "" {
			where = "no readable exe"
		}
		parts = append(parts, fmt.Sprintf("require unmet: %s is none of exe_base%v exe_glob%v",
			where, d.Require.ExeBase, d.Require.ExeGlob))
	} else {
		if len(d.Comm) > 0 {
			parts = append(parts, fmt.Sprintf("comm %s not in %v", quoteOrNone(p.CommBase()), d.Comm))
		}
		if len(d.Argv0) > 0 {
			names := quoteOrNone(p.Argv0Base())
			if run != "" {
				names += " and run token name " + quoteOrNone(BaseName(run))
			}
			parts = append(parts, fmt.Sprintf("argv0 %s not in %v", names, d.Argv0))
		}
	}
	if len(d.ExeGlob) > 0 {
		if p.Exe == "" {
			parts = append(parts, fmt.Sprintf("exe_glob%v had no exe to match", d.ExeGlob))
		} else {
			parts = append(parts, fmt.Sprintf("exe %q matched none of %v", p.Exe, d.ExeGlob))
		}
	}
	if len(d.ArgvPath) > 0 {
		if run == "" {
			parts = append(parts, fmt.Sprintf(
				"argv_path%v was not consulted: this process is not an interpreter, so its arguments are not its identity",
				d.ArgvPath))
		} else {
			parts = append(parts, fmt.Sprintf("run token %q matched none of %v", run, d.ArgvPath))
		}
	}
	if len(parts) == 0 {
		return "manifest names no predicate"
	}
	return strings.Join(parts, "; ")
}

func quoteOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return fmt.Sprintf("%q", s)
}
