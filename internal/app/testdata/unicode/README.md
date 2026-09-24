# Unicode emoji data

`emoji-data.txt` is taken verbatim from the Unicode Character Database, Unicode
17.0.0. It is the input to `TestNoEmojiInSourceStrings` in
`internal/app/no_emoji_source_test.go`, which fails on any character with an
emoji property in a string or rune literal of tuios's own Go source.

| File | Source | Used for |
| --- | --- | --- |
| `emoji-data.txt` | <https://www.unicode.org/Public/17.0.0/ucd/emoji/emoji-data.txt> | UTS #51 emoji properties |

Retrieved 2026-09-24.

## Licence

Published by Unicode, Inc. under the Unicode License v3, which permits
redistribution with the copyright and permission notice intact. The notice is
in the file's header comment and has not been altered.

    Copyright © 2025 Unicode, Inc.
    https://www.unicode.org/terms_of_use.html

## Why it is checked in

The same reason as `internal/vt/testdata/unicode`: a test that downloads its
input fails when the network does, and a new Unicode version would silently
change what it asserts. An upgrade is a visible commit.
