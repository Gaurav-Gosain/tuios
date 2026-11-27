# Runs on the Mac through ssh, as "sh -s". When the engine does not
# answer, it prints one line that says why and what to do. When the engine
# answers, it prints nothing.
dir="${SRTERM_DIR:-$HOME/.local/state/srterm}"
port="${SRTERM_PORT:-8080}"
t=$(cat "$dir/token" 2>/dev/null)
if curl -fsS -m 2 -o /dev/null -H "Authorization: Bearer $t" "http://127.0.0.1:$port/capabilities" 2>/dev/null; then
	exit 0
fi
log=$(tail -n 5 "$dir/engine.log" 2>/dev/null | tr '\n' ' ')
case "$log" in
*"display changed"*)
	echo "The Mac display went away (lid shut?). Open the lid or connect a screen, then restart the relay: press Ctrl+\\ and open the remote desktop again." ;;
*permission*)
	echo "The Mac has not given relay-engine the Screen Recording and Accessibility permissions. Turn them on in System Settings, Privacy & Security." ;;
*)
	echo "The engine on the Mac stopped. Its log says: $(printf '%s' "$log" | cut -c1-160)" ;;
esac
