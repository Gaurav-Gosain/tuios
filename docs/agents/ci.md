# CI and e2e test selection

What CI runs on a pull request, how the e2e selector picks tests, and how the suite is split into shards.

## What CI runs on a pull request

`.github/workflows/ci.yml` starts with a changes job. The job runs
`scripts/ci-changes.sh`, which reads the diff and names the jobs and the Go
packages the change can affect. A package is affected when it changed, or when
its test binary builds a package that changed. A job that the changes job does
not name shows as skipped. A change to a module file beyond the library pins,
to a CI file, or to a path no rule names runs everything. A push to main runs
every job, except ghostty-vt, learn-web, clienttests, the image build and the
Nix vendor hash. Those run on main only when the push changed their inputs.
The e2e suite selects its own tests (see "Which e2e tests a pull request runs" below). See what a branch
runs:

```bash
CHANGES_BASE=origin/main scripts/ci-changes.sh
```

The mixed-versions job pairs this build with the v0.12.1 release, the
previous minor (`scripts/e2e-mixed-versions.sh NEW OLD`). A client and a
daemon one minor release apart must work together. The job runs when a change
reaches the packages `internal/session` builds from, and on main. Move its
release with each minor release.

CI runs each test once. A failed test fails the job, and no job retries it.
A test that fails now and then has a bug: find the cause and fix it. Do not
rerun the job until it passes.

## Which e2e tests a pull request runs

A pull request runs only the e2e tests its change can reach. `e2e/tui/testselect`
reads the diff and a test map, which records the functions each test ran in a
coverage run. The e2e-tui test map workflow builds the map every night and
keeps it as the `e2e-test-map` artifact. CI reads the newest one from main.
The committed `e2e/tui/testselect/testmap.txt` is an old fallback. CI files,
the harness, `go.mod` beyond library pins, and any path no rule names run the
whole suite.

What the selector does with a change:

- A changed function selects the tests that ran it. A changed function that
  no test ran selects nothing, and the selector reports no error. When your
  change needs e2e cover, add to a test that reaches the code. A changed test
  file selects the tests it changes.
- A changed comment selects no test. The doc comment of a test selects that
  test. A change above the package clause of a test file runs the whole suite.
- A change to the unexported fields of a struct selects the tests that ran a
  function that names one of those fields. Any other change to a type
  selects the tests that ran a function that names the type.
- A test newer than the map runs on every change that reaches the binary,
  until the next night's map knows it.
- A draft pull request skips the shards. Mark it ready for review to run them.
- Main runs the whole suite. It skips the run only when the same commit
  already passed the whole suite as a pull request head.

Run the selection with the current map:

```bash
id=$(gh run list -R Gaurav-Gosain/tuios --workflow e2e-tui-map.yml --branch main \
  --status success --limit 1 --json databaseId --jq '.[0].databaseId')
rm -rf .dev/map && gh run download "$id" -R Gaurav-Gosain/tuios -n e2e-test-map -D .dev/map
cd e2e/tui && TUIOS_E2E=1 go test -list '^Test' . |
  go run ./testselect -base origin/main -map ../../.dev/map/testmap.txt
```

The whole suite is split into E2E_SHARDS shards of E2E_LANES lanes by the
measured times in `e2e/tui/shardplan/durations.txt`, and the shard plan job
fails when a lane is over E2E_LANE_LIMIT. Refresh the file (its header has the
command) when that job fails, or when it reports more than about 50 tests
without a measured time; a new test counts at the mean until then. Add a shard
when a fresh file still leaves lanes within a minute of the limit.

## Keep main green

Main runs every Go test job and the whole e2e suite, so it catches what a
pull request's selection left out. The idle-cost tests (`TestIdleCostStaysLow`,
`TestDockIdleCostWithComponentsStaysLow` and the like) and the timing budgets
in the unit tests fail there first. When one fails on main after your change
lands, fix the cause at once. Do not retry it away or loosen its budget to
make it pass.

A timing test waits on a condition, never on a fixed sleep. A clock budget
compares the fastest of several short runs, because load only adds time, and
does not apply under `-race`.
