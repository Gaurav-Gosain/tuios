# Generated files

The packages that embed a packed archive, and the two parts of a JSON verb.

Some packages embed an archive that `go generate` packs from the files beside
it: `skills/` (`skills/tuios.tar.gz`), `internal/learn/lessons`,
`harness/` (the bundled manifests, in the library), `internal/integration`
and `internal/session` (the verb docs). Run `go generate` in the
package after you edit those files. A test in each package fails when the
archive and the files differ. Rebase with `scripts/rebase.sh origin/main`. It
regenerates every packed archive at each stop where only archives conflict.

A JSON verb has two parts. The Go registry entry holds the handler, the scope,
the link need, the stream, and each parameter and result field with its name,
type, accepted values and default. The text is in the `.verbs.json` file
beside the Go file that registers the verb: the description, the description
of each parameter and result field, and the examples. For example,
`internal/session/verb_files.go` registers the file verbs, and
`internal/session/verb_files.verbs.json` holds their text. Edit both in one
change, then run `go generate ./internal/session`.
