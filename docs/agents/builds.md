# Builds: inlining, the ghostty backend, Nix and Docker

The noinline package list, the tests of the ghostty backend, and the Nix and Docker builds.

## Inlining off in a shipped build

A shipped tuios is built with inlining off in the packages
`scripts/noinline-packages.txt` lists, which saves about 1.1 MB. Every build
of `./cmd/tuios` reads that list: a shell line as
`GOFLAGS="$(scripts/goflags.sh)" go build ...`, GoReleaser through
`TUIOS_GOFLAGS`, Go code through the file, the e2e suite
through `goBuild`. A package joins the list only when nothing on a hot path
(drawing a frame, input, the pty read loop, the daemon's pane streams) calls
into it. `go install ...@vX` and the Nix package do not read the list and build the larger binary.

## Tests of the ghostty backend

```bash
# Run the tests of the ghostty backend. `go build -tags ghostty ./...` compiles
# it and runs none of its tests, so a change that passes the plain suite can
# still be wrong on the backend `scripts/install.sh ghostty` builds. The
# ghostty-vt job runs the packages whose tests link vt and that the change
# reaches; these are the ones that matter most, the renderer in internal/app
# included. `just ghostty-test` builds libghostty-vt first (needs zig).
PKG_CONFIG_PATH="$PWD/.ghostty-vt/native/pkgconfig" \
  go test -tags ghostty -count=1 -short \
    ./vt/ ./internal/session/ ./internal/terminal/ \
    ./internal/app/ ./internal/input/ ./cmd/tuios/
```

## Development with Nix

```bash
nix develop    # Enter development shell
nix build      # Build package
nix run        # Run directly
```

## Docker

```bash
docker build -t tuios .
docker run -it --rm tuios
```
