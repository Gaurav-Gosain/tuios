# Tape Recording

The recording guide lives on the docs site: https://tuios.dev/docs/tape-recording

Recording is in-app: `Ctrl+B T r` starts, `Ctrl+B T s` stops. Tapes land in the directory `tuios tape dir` prints, `$XDG_DATA_HOME/tuios` (`~/.local/share/tuios` on Linux), and are managed with `tuios tape list`, `show`, `play`, `delete`. There is no `tape record` or `tape run` subcommand.

A recording keeps every action you run with a key as `Action <name>`, so it plays back the same way. Keys you type into a dialog are not recorded.
