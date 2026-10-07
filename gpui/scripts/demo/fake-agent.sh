#!/bin/bash
# Prints a still frame of a coding agent's terminal, for screenshots of the
# GUI against a demo daemon. It runs no agent. Usage: fake-agent.sh KIND
d=$'\e[2m'; r=$'\e[0m'; b=$'\e[1m'; g=$'\e[32m'; red=$'\e[31m'; y=$'\e[33m'; bl=$'\e[34m'; m=$'\e[35m'; c=$'\e[36m'
printf '\e[?25l'
frame() {
case "$1" in
codex)
cat <<T
${d}›${r} Cache the shaped rows per generation in the painter

${bl}•${r} Read ${c}crates/tuios-gpui/src/painter.rs${r}
${bl}•${r} Read ${c}crates/tuios-gpui/src/rowplan.rs${r}
${bl}•${r} Edit ${c}painter.rs${r} ${g}+42${r} ${red}-17${r}
  ${d}│${r} ${d}fn prepare(&mut self, screen: &Screen) {${r}
  ${d}│${r} ${g}+    let dirty = screen.rows.iter()${r}
  ${d}│${r} ${g}+        .filter(|r| r.gen != self.drawn[r.y]);${r}
  ${d}│${r} ${red}-    for row in &screen.rows {${r}

${bl}•${r} Ran ${b}cargo test -p tuios-gpui${r}
  ${d}└${r} ${g}test result: ok. 41 passed; 0 failed${r}

${bl}•${r} Ran ${b}cargo run --release -- --perf${r}
  ${d}└${r} scroll p50 1.9 ms  p95 3.1 ms  ${d}(was 4.8 ms)${r}

T
;;
claude-question)
cat <<T
${g}●${r} Read ${c}src/client.ts${r}
${g}●${r} Read ${c}src/errors.ts${r}
${g}●${r} Search ${d}"status === 429"${r} ${d}· 3 matches${r}

The client throws on any non-2xx response today, so a
rate limit surfaces as a generic ${b}HttpError${r}. Two options:

 ${d}1.${r} Retry 429 with backoff and honour ${b}Retry-After${r}
 ${d}2.${r} Surface a typed ${b}RateLimitError${r} to the caller

${b}Should the client retry on 429, or surface the error?${r}

${y}❯ 1. Retry with backoff${r}
  2. Surface the error
  3. Type something else

${d}Enter to select · ↑↓ to navigate · Esc to cancel${r}
T
;;
claude-done)
cat <<T
${g}●${r} Fixed the race in ${c}attach${r}: the reply hook now waits
  for the session's first state before it answers.

  ${c}internal/session/attach.go${r}      ${g}+18${r} ${red}-6${r}
  ${c}internal/session/attach_test.go${r} ${g}+41${r}
  ${c}e2e/tui/attach_test.go${r}          ${g}+12${r} ${red}-2${r}

${g}●${r} Ran ${b}go test ./internal/session/ -count=50${r}
  ${d}└${r} ${g}ok${r} 50/50 runs

${d}✻ Worked for 6m 40s${r}

${d}──────────────────────────────────────────────────${r}
${b}›${r}
${d}──────────────────────────────────────────────────${r}
T
;;
codex-error)
cat <<T
${bl}•${r} Ran ${b}cargo sqlx migrate run${r}
  ${d}└${r} ${red}error: while executing migrations:${r}
    ${red}error returned from database: relation "users" already exists${r}

${red}■${r} The migration failed. I stopped before touching the
  schema again. The database has 2 of 3 migrations applied.
T
;;
esac
}

# Like a real agent, redraw the whole frame when the pane changes size.
redraw() {
	printf '\e[H\e[2J'
	frame "$1"
	printf '\e7'
}
trap 'redraw "$1"' WINCH
redraw "$1"
if [ "$1" = codex ]; then
	# A working agent redraws its timer; tuios marks a silent one as stalled.
	s=192
	while :; do
		printf '\e8\e[K%sWorking%s %s(%dm %02ds · esc to interrupt)%s' "$bl" "$r" "$d" $((s / 60)) $((s % 60)) "$r"
		s=$((s + 1))
		sleep 1 &
		wait $!
	done
fi
while :; do
	sleep 3600 &
	wait $!
done
