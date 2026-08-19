# Unicode test data

Two data files taken verbatim from the Unicode Character Database, Unicode
17.0.0. They are inputs to the tests in `internal/vt/unicode_*_test.go`.

| File | Source | Used for |
| --- | --- | --- |
| `GraphemeBreakTest.txt` | <https://www.unicode.org/Public/UCD/latest/ucd/auxiliary/GraphemeBreakTest.txt> | UAX #29 grapheme cluster boundaries |
| `EastAsianWidth.txt` | <https://www.unicode.org/Public/UCD/latest/ucd/EastAsianWidth.txt> | UAX #11 East Asian Width property |

Retrieved 2026-08-19.

## Licence

Both files are published by Unicode, Inc. under the Unicode License v3, which
permits redistribution with the copyright and permission notice intact. The
notice is in the header comment of each file and has not been altered.

    Copyright © 2025 Unicode, Inc.
    https://www.unicode.org/terms_of_use.html

## Why these are checked in rather than fetched

A test that downloads its own input fails when the network does, and silently
changes what it asserts when Unicode publishes a new version. Pinning the files
means a Unicode upgrade is a visible commit that shows exactly which characters
changed class.

## What the tests do with them

`GraphemeBreakTest.txt` is used as a corpus of adversarial input rather than as
an oracle. The emulator delegates clustering to the segmenter in
`github.com/charmbracelet/x/ansi`, which tracks its own Unicode version, so
holding the emulator to this file would mostly measure that library's version
skew. What the tests assert instead is the property the emulator owns: a cluster
its own segmenter recognises must occupy one cell group, must not be split by a
write boundary, and must survive a resize.

The skew itself is reported by `TestUnicode_SegmenterAgreesWithUAX29`, which
counts disagreements and prints them, so an upgrade of either side is visible
without failing the build over a library's release schedule.
