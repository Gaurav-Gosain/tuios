# nitpick

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/nitpick.svg)](https://pkg.go.dev/tuios.dev/nitpick)

`tuios.dev/nitpick` finds Go hygiene problems that a compiler and most
linters let through. Each rule is a plain
[go/analysis](https://pkg.go.dev/golang.org/x/tools/go/analysis) analyzer.
The `nitpick` command runs the rules and can report only the lines that a
change touched.

## Rules

| Rule | Default | Precision | What it reports |
|------|---------|-----------|-----------------|
| `swallowed_error` | off | 10% | `_ = f()` that drops an error in a function that returns an error, and an empty `if err != nil {}` |
| `error_to_empty_success` | on | 55% | `if err != nil { return nil, nil }` and other empty returns with a nil error |
| `config_fallback` | off | 0% | an environment variable that falls back to a development value, such as localhost |
| `narrator_comment` | on | 85% | a comment that says what the next line says |
| `deep_nesting` | on | 65% | control nesting of 5 or more in one function |
| `elision_comment` | off | 75% | a comment that stands in for missing code: "rest of implementation" |
| `junk_identifier` | off | 33% | a name such as foo, stuff or data2 |
| `commented_out_code` | on | 61% | Go code kept in a comment |
| `nolint_without_reason` | off | 20% | a `//nolint` directive with no reason |

Precision is the share of findings that the labeller judged worth a change,
on a labelled sample of real Go code. A rule is on by default when its
precision is 50% or more on a sample of 10 or more. [PRECISION.md](PRECISION.md) gives the
method and the numbers. A comment with a reason silences most rules: write
why the error is dropped, and the finding goes away.

## Command

```sh
go install tuios.dev/nitpick/cmd/nitpick@latest

nitpick check ./...                 # every finding in the packages
nitpick diff                        # findings on the lines changed since HEAD
nitpick diff --base origin/main     # findings on the lines this branch changed
nitpick diff --staged --gate 90%    # exit 1 when a finding of 90% precision or more is staged
nitpick rules                       # the rules and their precision
```

`nitpick diff` reads `git diff`. Without `--staged`, it uses the working
tree, and an untracked Go file counts as new. With `--base REF`, it starts
at the merge base of REF and HEAD. Without packages, it checks the packages
of the changed files.

Each finding shows the precision of its rule:

```
store/load.go:42:3: error_to_empty_success: the err != nil branch returns empty values and a nil error, so the caller sees a success (precision 55%)
```

| Flag | Meaning |
|------|---------|
| `--json` | Write JSON: `findings` (file, line, column, rule, message, precision), `errors`, `gate_failed`. |
| `--gate P` | Exit with 1 when a finding has a precision of P or more. P is `0.9` or `90%`. |
| `--enable LIST` | Turn on more rules. LIST is rule names with commas, or `all`. |
| `--disable LIST` | Turn off rules. |
| `--tests=false` | Do not check `_test.go` files. |

The exit status is 0 when the gate passes, 1 when it fails, and 2 on an
error, such as a package that does not compile.

## Library

```go
import "tuios.dev/nitpick"

for _, r := range nitpick.Rules {
	fmt.Println(r.Name(), r.Default, r.Precision)
}
```

`nitpick.Analyzers()` returns the default analyzers, ready for
`multichecker.Main` or any other driver. Each analyzer is also a variable
of its own, such as `nitpick.SwallowedError`.

The idea of a diff-scoped hygiene check with a measured precision comes from
[nits](https://github.com/jkomyno/nits). nitpick shares no code with it.
