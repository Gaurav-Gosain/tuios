# Goldens

Each SVG in this folder is the output of pele for one source of
`testdata/corpus`. The name is `<corpus file>-<index>.svg`.

The sources are the examples of the Mermaid documentation, which pele keeps in
`tests/corpus`. Mermaid is under the MIT licence, copyright Knut Sveidqvist and
the Mermaid contributors. pele is under the MIT licence, copyright kepano.

`gen.mjs` writes the goldens again. Run it by hand against a build of pele at
the commit that the port follows:

```sh
cd pele && git checkout 458322a5b8abb1ae57ddf127c32017ab9fb14a92
npm ci && npm run build
node /path/to/sketch/testdata/golden/gen.mjs /path/to/pele /path/to/sketch/testdata/golden
```

Do not write a golden from the Go output.

## Time zone

pele reads gantt dates in the local time zone of the process, as JavaScript
does. The gantt goldens were written in `Asia/Dubai`. Under `TZ=UTC`,
`gantt-002.svg` comes out different.

sketch reads gantt dates in `RenderOptions.Location`. A nil location means
`time.Local`, which follows the zone of the process as pele does. `TestGolden`
sets `Location` to `Asia/Dubai`, so the test passes on a machine in any zone.
The test binary embeds the zone data, so the machine does not need it.

To write the gantt goldens again, run `gen.mjs` with `TZ=Asia/Dubai`.
