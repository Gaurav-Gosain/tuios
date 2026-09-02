## What does this pull request do?

Please include a summary of the change and which issue (if any) is referenced:
- Resolves: #<issue-number> (if applicable)
- Description: <brief description>

---

## Motivation / Context

Why is this change needed? What problem does it solve?  
(e.g., bug fix, new feature, performance improvement, refactor)

---

## What has been changed

List out key changes:
- Added/changed file `…`
- Updated logic in `cmd/tuios`
- Added tests for `<component>`
- Updated docs: `docs/…`

---

## How to verify this change

Include steps to test this on different platforms/install methods:
1. Clone and build: `go build ./cmd/tuios`
2. Run on Linux (x86_64) / Docker etc.
3. Verify the behavior: …
4. For installers, test via Homebrew or AUR if relevant.

---

## Notes on compatibility / installation method

- Platform(s) tested: (e.g., Linux arm64, Windows x86_64)  
- Installation method tested: (e.g., Homebrew, Bash script, Docker)  
- Version/commit used: `<commit-hash>`  
- Any limitations or blockers: `<description>`

---

## Negative controls

A test that has never been seen to fail is not evidence. For each test you
added, break the thing it covers and paste the failure it prints.

Two rules, from three green suites that hid three real defects. See
`e2e/tui/NEGATIVE_CONTROLS.md`.

- [ ] **The control deletes the call site, not the function.** Cut the `case`,
      the `Register`, the route or the gate line. A control on the function
      proves the test and the function match. It does not prove anything calls
      the function.
- [ ] **Every negative test has its positive half.** A test that asserts "X does
      not happen when Y" also asserts, in the same fixture, "X happens when not
      Y". Without it, the test may never have reached Y.

Control run:
- Wiring cut: `<file:line, what you removed>`
- Test that failed: `<name, and the message it printed>`

---

## Checklist

- [ ] Code changes compile and tests pass
- [ ] Added/Updated relevant documentation
- [ ] Tested across supported platforms and install methods
- [ ] Added or updated tests
- [ ] PR title follows format (e.g., `[BUG]`, `[FEATURE]`, etc.)

---

## Additional Context (optional)

Add any screenshots, logs, or references to other issues if applicable.
