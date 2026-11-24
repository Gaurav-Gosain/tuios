# Browser build (Learn tuios)

The WebAssembly build behind tuios.dev/learn and the lesson data.

`cmd/tuios-wasm` is tuios compiled to WebAssembly for the guided tour at
tuios.dev/learn: the real app in Learn mode, with a fake shell
(`internal/webshell`) in every pane and an event stream for lessons
(`internal/learn`). Browser-only code is behind `js` build tags or in those
three directories, so the native build is unchanged. The page API and event
contract are in [cmd/tuios-wasm/README.md](../../cmd/tuios-wasm/README.md).

The lessons are data in `internal/learn/lessons`: `tracks.json` and one
`tracks/<id>.json` per track, with a rule per step that says when it is done.
The lesson engine there is the only one, and the page draws its `lesson.*`
events. Edit a track there, never in tuios-docs, then run `go generate
./internal/learn/lessons`, which packs the files into the archive the binary
embeds. The `tuioslint` gate checks every action, overlay, event and key a lesson names, so a rename that breaks
a lesson fails the change that renames it.

```bash
cmd/tuios-wasm/build.sh out/                  # the files the docs site needs
node cmd/tuios-wasm/serve.mjs out/ 8765       # try it at http://127.0.0.1:8765
go test ./internal/webshell/ ./internal/learn/...  # native tests of the Go side
node cmd/tuios-wasm/smoke-lessons.mjs out/ frames/ basics  # a track by show me
```

A change to `internal/app` or `internal/input` can break the js build without
breaking any native one. The learn-web job builds it on every pull request
that reaches the js build.
