# Usage-limit screens

Each fixture is the text a harness shows when a usage limit stops it, and what
tuios does with it today. The header says where the text came from:

- `from source`: a string in the harness's own source or published binary.
  The words are exact. The frame around them is a plausible layout, not a capture.
- `from user report`: text a third party kept in a fixture or an issue.
- `captured`: bytes read from a real run. None here, because a real limit
  needs a spent plan.

`today` is what `Classify` returns now: a state, or `none` for no rule matching.
`resets` is what `LimitReset` reads from the line the rule matched: the
harness's words for when the limit ends, or `none`. A change to a manifest's
limit rule or to `LimitReset` changes these in the same commit.

Every fixture reads as `limit_reached`. Before the limit rules, every one read
as `idle`, because the empty prompt box under the message matched the idle
rule. The frame is not captured, so this shows the rule order, not a measured
screen.
