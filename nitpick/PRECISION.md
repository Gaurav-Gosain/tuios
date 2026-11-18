# Precision

Each nitpick rule carries a precision: the share of its findings that the
labeller judged worth a change. The command shows it with each finding, and
`--gate` uses it. This file gives the method and the numbers.
[precision/labels.tsv](precision/labels.tsv) holds every label.

## Defaults

A rule is on by default when its precision is 50% or more on a sample of 10
or more. A new measurement moves a default by the same rule.

## Numbers

| Rule | Default | Sample | Correct | Precision | 95% interval | Findings in the corpus |
|------|---------|--------|---------|-----------|--------------|------------------------|
| `swallowed_error` | off | 40 | 4 | 10% | 4% to 23% | 310 |
| `error_to_empty_success` | on | 11 | 6 | 55% | 28% to 79% | 11 |
| `config_fallback` | off | 1 | 0 | 0% | 0% to 79% | 1 |
| `narrator_comment` | on | 20 | 17 | 85% | 64% to 95% | 1509 |
| `deep_nesting` | on | 40 | 26 | 65% | 50% to 78% | 762 |
| `elision_comment` | off | 4 | 3 | 75% | 30% to 95% | 4 |
| `junk_identifier` | off | 6 | 2 | 33% | 10% to 70% | 6 |
| `commented_out_code` | on | 36 | 22 | 61% | 45% to 75% | 61 |
| `nolint_without_reason` | off | 20 | 4 | 20% | 8% to 42% | 107 |

The interval is the Wilson score interval. A sample under 20 gives a wide
interval, so read those numbers as a first estimate:

- `config_fallback` found 1 case in the whole corpus, in test support code.
  Published libraries read their settings in other ways, and the corpus has
  few services. The rule needs a corpus of services before its number means
  much.
- `error_to_empty_success`, `elision_comment` and `junk_identifier` found
  11, 4 and 6 cases. The sample is every finding.

## Method

1. The corpus is real Go code:
   - tuios at commit `c2ac3d00b54d`: the app module and its 10 library modules,
     test files included.
   - The Go 1.27.4 standard library, without test files.
   - 36 modules from the Go module proxy at their latest version on
     2026-11-18, without test files. Libraries: cobra, gin, chi, logrus,
     urfave/cli, prometheus client_golang, go-redis, nats.go, consul api,
     docker cli, caddy, pgx, the OpenTelemetry OTLP gRPC exporter, minio-go,
     vault api, kafka-go, mongo-driver, viper, the MCP Go SDK, mcp-go,
     weaviate-go-client, ch-go, pprof, x/tools and azure-sdk-for-go internal.
     Services: nats-server, pocketbase, fiber, miniflux, gotify, navidrome,
     shiori, filebrowser, memos, ntfy and listmonk. Packages that did not
     load (cgo, an old go line) are left out.
2. `nitpick check --enable all --json` ran on the corpus.
3. A random sample was drawn per rule: up to 40 findings, spread evenly over
   tuios, the standard library and the modules, with at most 2 findings from
   one file. A rule with fewer findings has all of them in the sample.
4. The author of this change, as the one labeller, read each finding in its
   source and labelled it. A finding is worth a change when a reviewer of new code would ask for that change:
   - `swallowed_error`: the dropped error can hide a failure that matters
     (lost data, wrong state, a silent no-op). Cleanup on a path that already
     returns an error, output to a terminal and a hash write are not.
   - `error_to_empty_success`: the caller cannot tell the failure from an
     empty result. A documented "not found means empty" is not.
   - `config_fallback`: code that runs in production uses the development
     value when the setting is missing.
   - `narrator_comment`: the comment says nothing that the next line does
     not say. A comment that names a grammar rule, a protocol field or the
     meaning of a magic value is not.
   - `deep_nesting`: the function reads better with an early return or an
     extracted part. A byte-level parser, a lexer or a hot path in the
     runtime, where the nesting follows the data, is not.
   - `elision_comment`: the comment stands in for a body that is missing.
   - `junk_identifier`: the name says nothing about the value.
   - `commented_out_code`: the comment is dead code. A formula, a signature
     or a sketch of syntax is not.
   - `nolint_without_reason`: a reader cannot tell why the suppression is
     safe. A style or complexity linter (gocyclo, nestif, mnd, wrapcheck) and
     a case that the code makes plain are not.

## Pilot and held-out samples

The first sample (the pilot) showed classes of false positives, and the
rules changed to leave them out:

- `swallowed_error` had 1 pilot finding of 20 that mattered. Most
  `_ = f()` lines are cleanup, output, or a call whose result is stored
  elsewhere. The rule now skips cleanup calls (Close, os.Remove, Kill, the
  deadline setters) and fmt.Fprint, and it reports `_ = f()` only in a
  function that returns an error.
- `narrator_comment` took the doc comment of a one-line function as a
  comment in its body, and it read formulas as narration.
- `commented_out_code` read formulas, signatures and labels as code. It now
  wants a whole comment group of code whose names mostly exist at that place.
- `nolint_without_reason` now takes a comment on the line above as the
  reason.
- `error_to_empty_success` now skips functions that return only an error.
  It also took `return nil, errFoo{}` as an empty result, which was a bug.
- `elision_comment` matched prose such as "for the rest of the function".
  A line of the comment must now be the marker and nothing more.
- `junk_identifier` no longer reports bar, dummy or numbered pairs.

Every pilot finding that the changes removed had the label "not worth a
change". Because the pilot steered the changes, a precision from it would be
too high. For the rules that changed and still had findings left to draw, a
new random sample (held out) came from the findings that no one had
labelled, and the table above uses it:

| Rule | Pilot, still reported | Held out |
|------|-----------------------|----------|
| `swallowed_error` | 0 of 1 | 4 of 40 |
| `narrator_comment` | 22 of 29 | 17 of 20 |
| `commented_out_code` | 16 of 21 | 22 of 36 |
| `nolint_without_reason` | 9 of 30 | 4 of 20 |

`deep_nesting` and `config_fallback` did not change, so the table uses
their pilot. For `error_to_empty_success`, `elision_comment` and
`junk_identifier`, the labelled findings are all the findings that the
final rules report.

## Measure again

1. Run nitpick on a corpus:
   `nitpick check --enable all --json ./... > findings.json`.
2. Draw a sample per rule as in step 3, and label it as in step 4.
3. Put the counts in `Rules` in rules.go and in the table above. A test
   fails when the two differ.
