#!/bin/bash
# Prints about 200 coloured log lines a second until killed: timestamps,
# counters and code words, the load a busy build or agent puts on a pane.
words=(cargo build painter shape cache row glyph atlas frame layout notify sidebar bridge daemon ghostty scroll)
n=0
while :; do
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		n=$((n + 1))
		w=${words[$((n % ${#words[@]}))]}
		printf '\e[2m%(%H:%M:%S)T\e[0m \e[3%dm%-8s\e[0m step %6d  %s::%s took \e[1m%d.%02d ms\e[0m\n' -1 $((n % 6 + 1)) "$w" "$n" "$w" "${words[$((n * 7 % ${#words[@]}))]}" $((n % 9)) $((n % 100))
	done
	sleep 0.05
done
