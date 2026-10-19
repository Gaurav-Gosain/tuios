# sketch

`tuios.dev/sketch` draws Mermaid diagrams as SVG, in pure Go.

It is a port of [pele](https://github.com/obsidianmd/pele) by kepano, under
the MIT licence. The port follows pele v1.6.0, commit
`458322a5b8abb1ae57ddf127c32017ab9fb14a92`. [LICENSE](LICENSE) has the notice
of pele and of the port.

## Status

The port is in progress. It draws flowcharts and sequence diagrams. Each other diagram type gives an
error with the code `unsupported-diagram` until its port is done.

The output is the same as the output of pele, byte for byte. The test suite
compares each diagram with an SVG that pele drew from the same source.

| Type | Goldens that match |
|---|---:|
| flowchart | 114/114 |
| sequence | 38/38 |

## Use

```go
svg, err := sketch.Render("flowchart LR\n  A --> B\n", sketch.RenderOptions{})
if err != nil {
	// err is an *sketch.Error with a code: syntax, semantic, limit or unsupported-diagram.
}
os.WriteFile("out.svg", []byte(svg.Data), 0o644)
```

## Layout of the code

Each Go file is the port of one pele file. The header of the file names it.
The Go files keep the names, the functions and the comments of pele, so that a
change upstream maps to a change here.

| Folder | pele folder |
|---|---|
| `.` | `src/core.ts`, `src/detect.ts`, `src/preprocess.ts` |
| `internal/types` | `src/types.ts`, `src/errors.ts` |
| `internal/text` | `src/text` |
| `internal/svg` | `src/svg` |
| `internal/layout` | `src/layout` |
| `internal/util` | `src/util` |
| `internal/diagrams/...` | `src/diagrams/...` |
| `internal/js` | none: JavaScript semantics the port needs |

`scripts/upstream.sh` lists the pele commits after the pin and the files that
each commit changes.

## Limits

The limits of pele apply. The source can have 50,000 characters. A flowchart can
have 5,000 edges. The SVG can have 4,000,000 characters. A host can set lower
limits in `RenderOptions`.

## Tests

```sh
go test ./...
```

`TestGolden` draws the 522 sources of the pele documentation corpus. It shows
how many match for each type. It fails when a diagram of a ported type does not
match.
