# Go libraries in this repository

The libraries published under tuios.dev, the workspace, and how to move a pin.

Some parts of tuios are Go modules of their own, published under
`tuios.dev/<name>` import paths. tuios.dev serves the `go-import` meta tag
that points the go command at the folder in this repository. They are
`progstatus/` (`tuios.dev/progstatus`), `vt/` (`tuios.dev/vt`), `swatch/`
(`tuios.dev/swatch`), `shot/` (`tuios.dev/shot`), `tml/` (`tuios.dev/tml`),
`plot/` (`tuios.dev/plot`), `loom/` (`tuios.dev/loom`), `sketch/`
(`tuios.dev/sketch`), `acp/` (`tuios.dev/acp`), `harness/`
(`tuios.dev/harness`), `nitpick/` (`tuios.dev/nitpick`) and `progresume/`
(`tuios.dev/progresume`). `go.work` lists
them all.

- `go.work` makes the app, `e2e/tui` and each library one workspace, so a
  change to a library and to the app lands in one pull request. Never add a
  `replace` for a library to `go.mod`: `go install .../cmd/tuios@vX` ignores
  `go.work` and must resolve the library by its pin.
- `go.mod` pins each library at a commit or a library tag. The pinned version
  must hold the same files as the folder. After a change to a library folder,
  commit it, then move the pin:

  ```bash
  GOWORK=off go get tuios.dev/progstatus@<commit>
  scripts/library-pins.sh sync    # point go.work at the pin, tidy both modules
  scripts/library-pins.sh check   # what CI and the release check
  ```

  `sync` also tidies `e2e/tui/go.mod`, which carries the same pins. A pin move also
  changes the Nix `vendorHash` in `tuios.nix`. The nix-vendor-hash job fails
  and prints the new hash to put there.
- Rebase a branch that moves a pin right before it lands, then move the pin
  again to the library commit on the rebased branch. A rebase gives that
  commit a new hash, so the old pin names a commit main does not hold. When
  main changed the library in between, the old pin also lacks that change.
  Either way `library-pins.sh check` fails on main.
- A new library joins `go.work` first. It gets its pin with the first change
  that imports it, because `go mod tidy` drops a requirement nothing imports.
  Until then `scripts/library-pins.sh` skips it. A library may require
  another one at a commit before tuios imports either, as loom and sketch
  require plot.
  `scripts/library-pins.sh proxy` serves that version too, so the tidy check
  needs no network for it.
- `./...` from the root stops at a nested module. Name a library to test it:
  `go test ./progstatus/... ./vt/... ./tml/...`.
- A library imports no `github.com/Gaurav-Gosain/tuios/...` package.
- A branch made before `internal/vt` became `vt/` crosses the move with
  `scripts/vt-rename.sh` after a rebase.
- A library tag has the folder as a prefix, such as `progstatus/v0.1.0`.
  Tagging is the maintainer's call.
