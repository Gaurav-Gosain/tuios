# Omarchy fixtures

- `themes/NAME/colors.toml`: the colors.toml of every theme Omarchy ships,
  from basecamp/omarchy at 246f33a2fef66411c990860d88e0823ae83987fc.
- `v3/tokyo-night/colors.toml`: Tokyo Night from Omarchy v3.8.4
  (8fcc9d6048af4cb0e3af8512c78049857a3b53dd), the layout before Omarchy 4.
- `current/`: a current directory with Tokyo Night applied, for the examples.
- `cases/NAME/`: files written for this package, for the parser's quirks.
- `resolved/NAME.tsv`: what `bin/omarchy-theme-color --file FILE --all`
  prints for each file above, at the same Omarchy commit. A `case-` prefix
  is a file from `cases/`, and `v3-` is the v3 file.

The Omarchy files are under the MIT licence in `LICENSE.omarchy`.

To regenerate `resolved/` from an Omarchy checkout at `$SRC`:

```sh
for d in themes/*/ cases/*/; do
  n=$(basename "$d"); [ "${d%%/*}" = cases ] && n=case-$n
  "$SRC/bin/omarchy-theme-color" --file "$d/colors.toml" --all >"resolved/$n.tsv" 2>/dev/null
done
"$SRC/bin/omarchy-theme-color" --file v3/tokyo-night/colors.toml --all >resolved/v3-tokyo-night.tsv
```
