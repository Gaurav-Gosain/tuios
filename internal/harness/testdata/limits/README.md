# Usage-limit screens

Each fixture is the text a harness shows when a usage limit stops it, and what
tuios does with it today. The header says where the text came from:

- `from source`: a string in the harness's own source or published binary.
  The words are exact. The frame around them is a plausible layout, not a capture.
- `from user report`: text a third party kept in a fixture or an issue.
- `captured`: bytes read from a real run. None here, because a real limit
  needs a spent plan.

`today` is what `Classify` returns now: a state, or `none` for no rule matching.
The fixtures document a gap. When a manifest learns to read limits, change
`today` in the same commit.

Today every fixture reads as `idle`, because the empty prompt box under the
message matches the idle rule and no rule knows the limit text. The frame is
not captured, so this shows the rule order, not a measured screen.
