# Fuzzing and conformance

The VT conformance corpus, the fuzz targets, and the differential test against tmux.

## Terminal Emulator Conformance

`vt/` carries a table-driven conformance corpus: an input byte
sequence, and the screen it should produce, on a screen small enough that a
diff is readable. It runs with the ordinary suite.

```bash
# The corpus, the unicode sweeps and the generated-input sweep
go test ./vt/

# Skips the exhaustive write-boundary sweep, which is the slow part
go test -short ./vt/
```

Three things in it are worth knowing about before adding a case:

- A case that leaves `unhandled` false asserts the emulator recognised every
  sequence in its input. A sequence the emulator ignores leaves a screen
  indistinguishable from one it handled by doing nothing, which is how NEL and
  DECALN stayed missing under green tests.
- A case marked `knownBug` is expected to fail, and the test complains if it
  starts passing, so a fixed bug cannot sit on the list pretending to still be
  one.
- The unicode sweeps are driven by pinned UCD data files under
  `vt/testdata/unicode/`, with provenance and licence in the README
  there.

## Terminal Emulator Fuzzing

`vt/vtgen` generates terminal input by grammar rather than by byte,
so the parser reaches the code past its ground state. A failing run reduces by
delta debugging to a script of named sequences a person can read.

```bash
# Deterministic seeds, part of the ordinary suite
go test ./vt/ -run TestVTGen

# Coverage-guided, for a real campaign
go test ./vt/ -run XXX -fuzz FuzzEmulatorScript -fuzztime 10m
```

## Fuzzing

Every `Fuzz*` target's seeds, and every file under a package's
`testdata/fuzz/`, run in the ordinary suite. The mutator runs only when asked,
and nightly in `.github/workflows/fuzz.yml`, which keeps each package's corpus
in the Actions cache and uploads the failing input when a target finds
something. `go test -list 'Fuzz' ./...` lists the targets. Besides the
emulator ones above:

- `internal/app`: `FuzzModel` drives the whole window manager through the
  `internal/fuzz` action alphabet; the clip and line-width targets check the
  compositor against `ansi.StringWidth`.
- `internal/session`: the frame reader, the gob payloads (decode, bound,
  round trip), the JSON verb dispatcher, and selectors.
- `harness` (tuios.dev/harness): `Classify` against `Explain` over the bundled
  manifests and over fuzzed ones.
- `internal/config` and `internal/tape`: key normalisation, the config
  pipeline, and the tape lexer and parser.
- `vt`: the kitty payload decoder and command parser, and with
  `-tags ghostty` the differential against libghostty-vt.

Turn a finding into a regression the suite can keep:

- A vtgen target prints the reduced script as JSON. Save it under
  `vt/testdata/vtgen-repros/` with a `why`. Do not keep the input
  bytes instead: they decode through the generator, and the next change to
  vtgen turns them into a different script that passes.
- `FuzzModel` and the other `internal/fuzz` runs print a `--- script ---`
  block. Save it under `internal/app/testdata/fuzz-repros/`.
- Any other target: keep the file `go test` wrote under `testdata/fuzz/`.

A repro printed with `NOT REPRODUCIBLE` came from a target whose replays
differ; fix the target before trusting the script.

## Differential Testing Against tmux

An independent implementation catches what a hand-written expectation cannot,
because a test can be wrong in the same way the code is. Needs the `tmux` binary
and spawns processes, so it is behind a build tag.

```bash
go test -tags differential ./vt/ -run TestDifferential -v
```

Cases where tmux is the one diverging are listed in `tmuxDiffers` with the
reasoning. The test fails if one of them starts agreeing, so the list cannot
rot.
