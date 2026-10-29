# OSC 7501 captures

Files here are raw bytes from real programs.

| File | Source | Kind |
|------|--------|------|
| claude-code-2.1.312-query.bin | Claude Code 2.1.312 at start-up, asking the terminal whether it supports OSC 7501 | captured |
| claude-code-2.1.312-idle.bin | The report Claude Code 2.1.312 sent after it showed its prompt | captured |

Capture method: Claude Code 2.1.312 ran under a pty with TERM_PROGRAM set to an
unknown value. A script answered the query with `ESC ] 7501 ; ? ESC \`. Nobody
typed a prompt, so the run made no model request. Only the OSC 7501 sequences
were kept, not the screen.

The same release builds its reports in a function that writes
`state=...:app=claude-code`, and `id`, `kind`, `progress`, `title` and `msg` when set.
It sends the query `OSC 7501 ; ?` and treats "no reply" as "do not rely on it".

Pi 0.73.1 (npm package `@mariozechner/pi-coding-agent`) holds no `7501` string in
its own files, so it does not emit OSC 7501 in that version.
