// Command tml checks, formats and describes TML documents.
//
//	tml validate [--json] [--person] [FILE...]
//	tml fmt [--json | --xml] [-w] [FILE]
//	tml schema [-o FILE]
//
// FILE - or no FILE reads standard input.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"tuios.dev/tml"
)

const usage = `tml checks, formats and describes TML documents.

Usage:
  tml validate [--json] [--person] [FILE...]   Check documents against the catalog.
  tml fmt [--json | --xml] [-w] [FILE]         Print a document in canonical form.
  tml schema [-o FILE]                         Print the JSON Schema of TML JSON.

FILE - or no FILE reads standard input.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "validate", "lint":
		return validate(args[1:], stdin, stdout, stderr)
	case "fmt":
		return format(args[1:], stdin, stdout, stderr)
	case "schema":
		return schema(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "tml: %q is not a command. Run tml help.\n", args[0])
	return 2
}

func read(name string, stdin io.Reader) ([]byte, error) {
	if name == "" || name == "-" {
		return io.ReadAll(io.LimitReader(stdin, 16<<20))
	}
	return os.ReadFile(name)
}

func validate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the problems as the protocol's JSON error object")
	person := fs.Bool("person", false, "use the larger document limit of the person's own command line")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}
	lim := tml.DefaultLimits()
	if *person {
		lim = tml.PersonLimits()
	}
	status := 0
	for _, f := range files {
		src, err := read(f, stdin)
		if err != nil {
			fmt.Fprintf(stderr, "tml: %v\n", err)
			status = 1
			continue
		}
		_, problems, err := tml.Check(src, &tml.ValidateOptions{Limits: &lim})
		if err != nil {
			status = 1
			var te *tml.Error
			if !errors.As(err, &te) {
				fmt.Fprintf(stderr, "tml: %s: %v\n", f, err)
				continue
			}
		}
		if *asJSON {
			out := map[string]any{"file": f, "valid": err == nil, "problems": problems}
			if problems == nil {
				out["problems"] = []tml.Problem{}
			}
			b, _ := json.Marshal(out)
			fmt.Fprintln(stdout, string(b))
			continue
		}
		for _, p := range problems {
			fmt.Fprintf(stdout, "%s:%s\n", f, problemText(p))
		}
		if err == nil {
			fmt.Fprintf(stdout, "%s: valid\n", f)
		}
	}
	return status
}

func problemText(p tml.Problem) string {
	var b strings.Builder
	if p.Line > 0 {
		fmt.Fprintf(&b, "%d:%d: ", p.Line, p.Col)
	} else {
		b.WriteString(" ")
	}
	if p.Severity == tml.SevWarning {
		b.WriteString("warning: ")
	}
	b.WriteString(p.Message)
	at := p.At
	if at == "" {
		at = "/"
	}
	fmt.Fprintf(&b, " [%s at %s]", p.Code, at)
	if p.DidYouMean != "" {
		fmt.Fprintf(&b, "\n    did you mean: %s", p.DidYouMean)
	}
	if len(p.Accepted) > 0 && len(p.Accepted) <= 30 {
		fmt.Fprintf(&b, "\n    accepted: %s", strings.Join(p.Accepted, ", "))
	}
	if len(p.Available) > 0 {
		fmt.Fprintf(&b, "\n    available: %s", strings.Join(p.Available, ", "))
	}
	return b.String()
}

func format(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	toJSON := fs.Bool("json", false, "print TML JSON")
	toXML := fs.Bool("xml", false, "print TML text")
	write := fs.Bool("w", false, "write the result back to FILE")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *toJSON && *toXML {
		fmt.Fprintln(stderr, "tml fmt: give --json or --xml, not both.")
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "tml fmt: give one FILE.")
		return 2
	}
	name := fs.Arg(0)
	if *write && (name == "" || name == "-") {
		fmt.Fprintln(stderr, "tml fmt: -w needs a FILE.")
		return 2
	}
	src, err := read(name, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "tml: %v\n", err)
		return 1
	}
	lim := tml.PersonLimits()
	doc, err := tml.Parse(src, &tml.ParseOptions{Limits: &lim})
	if err != nil {
		var te *tml.Error
		if errors.As(err, &te) {
			for _, p := range te.Problems {
				fmt.Fprintf(stderr, "%s:%s\n", displayName(name), problemText(p))
			}
		} else {
			fmt.Fprintf(stderr, "tml: %v\n", err)
		}
		return 1
	}
	asJSON := tml.IsJSON(src)
	if *toJSON {
		asJSON = true
	}
	if *toXML {
		asJSON = false
	}
	var out []byte
	if asJSON {
		out = tml.FormatJSON(doc, true)
	} else {
		out = tml.FormatXML(doc)
	}
	if *write {
		mode := os.FileMode(0o600)
		if st, err := os.Stat(name); err == nil {
			mode = st.Mode().Perm()
		}
		if err := os.WriteFile(name, out, mode); err != nil {
			fmt.Fprintf(stderr, "tml: %v\n", err)
			return 1
		}
		return 0
	}
	_, _ = stdout.Write(out)
	return 0
}

func displayName(name string) string {
	if name == "" {
		return "-"
	}
	return name
}

func schema(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write the schema to this file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s := tml.Schema()
	if *out != "" {
		if err := os.WriteFile(*out, s, 0o644); err != nil { //nolint:gosec // the schema is public, and go generate commits it
			fmt.Fprintf(stderr, "tml: %v\n", err)
			return 1
		}
		return 0
	}
	_, _ = stdout.Write(s)
	return 0
}
