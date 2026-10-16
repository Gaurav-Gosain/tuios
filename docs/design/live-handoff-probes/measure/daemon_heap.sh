#!/bin/bash
# Usage: daemon_heap.sh <variant: idle|print1000> <port>
# Throwaway, isolated daemon (own XDG dirs, HOME and --server name). Kills only what it started.
set -u
M=$(cd "$(dirname "$0")" && pwd)
BIN=$M/tuios
VARIANT=$1
PORT=$2
BASE=$(mktemp -d /tmp/hm.XXXX)
for k in XDG_RUNTIME_DIR XDG_CONFIG_HOME XDG_STATE_HOME XDG_CACHE_HOME XDG_DATA_HOME XDG_CONFIG_DIRS XDG_DATA_DIRS HOME; do
  mkdir -p "$BASE/$k"; export $k="$BASE/$k"
done
export SHELL=/bin/sh ENV= PS1='$ '
SRV=hm$$
T() { "$BIN" --server "$SRV" "$@"; }

"$BIN" daemon --server "$SRV" --pprof 127.0.0.1:$PORT >"$BASE/daemon.out" 2>&1 &
DPID=$!
trap 'kill $DPID 2>/dev/null; sleep 0.5; kill -9 $DPID 2>/dev/null; pkill -9 -f "$BASE" 2>/dev/null; rm -rf "$BASE"' EXIT
for _ in $(seq 1 100); do curl -sf "http://127.0.0.1:$PORT/debug/pprof/" >/dev/null && break; sleep 0.1; done

heap() {
  local tag=$1
  curl -sf "http://127.0.0.1:$PORT/debug/pprof/heap?gc=1&debug=1" >"$BASE/heap.txt"
  local inuse alloc sys rss
  inuse=$(rg -o '# HeapInuse = (\d+)' -r '$1' "$BASE/heap.txt")
  alloc=$(rg -o '# HeapAlloc = (\d+)' -r '$1' "$BASE/heap.txt")
  sys=$(rg -o '# Sys = (\d+)' -r '$1' "$BASE/heap.txt")
  rss=$(ps -o rss= -p $DPID | tr -d ' ')
  echo "$VARIANT ${W:-80}x${H:-24}|$tag|HeapAlloc=$((alloc/1000))KB|HeapInuse=$((inuse/1000))KB|Sys=$((sys/1000))KB|RSS=${rss}KiB"
}

heap "0 panes"
GEN='awk "BEGIN{for(i=1;i<=1000;i++){s=sprintf(\"\033[38;5;%dm%05d \",16+i%200,i); while(length(s)<190) s=s \"content word \"; print s \"\033[m\"}}"; exec sh'
W=${W:-80}; H=${H:-24}
TUIOS_SERVER=$SRV "$M/hmprobe" mem $W $H >/dev/null
if [ "$VARIANT" = print1000 ]; then T send-text -s mem "$GEN
" >/dev/null; fi
for i in $(seq 2 60); do
  if [ "$VARIANT" = print1000 ]; then
    T new-window -s mem "w$i" -- sh -c "$GEN" >/dev/null
  else
    T new-window -s mem "w$i" >/dev/null
  fi
done
sleep 3
heap "60 panes t+3s"
sleep 32
heap "60 panes t+35s (after 30s release loop)"
T send-text -s mem -w w60 'stty size
' >/dev/null; sleep 1; echo "w60 stty: $(T capture-pane -s mem -w w60 2>/dev/null | rg -o '^\d+ \d+$' | tail -1)"
cp "$BASE/heap.txt" "$M/heap_${VARIANT}_${W}x${H}.txt"; curl -sf "http://127.0.0.1:$PORT/debug/pprof/heap?gc=1" -o "$M/heap_${VARIANT}_${W}x${H}.pb.gz"
echo "children: $(pgrep -P $DPID | wc -l | tr -d ' ')"
