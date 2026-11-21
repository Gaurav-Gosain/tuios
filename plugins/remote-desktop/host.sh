# Runs on the host, through ssh, as "sh -s". It makes sure a screen-relay
# engine runs on 127.0.0.1:8080 and prints:
#   port=PORT
#   token=TOKEN        (when the engine has one)
#   error=TEXT         (when it cannot start)
# Nothing else goes to stdout.

PATH="/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$PATH"
port=8080

ready() {
	if [ -n "${1:-}" ]; then
		curl -fsS -m 2 -o /dev/null -H "Authorization: Bearer $1" "http://127.0.0.1:$port/capabilities" 2>/dev/null
	else
		curl -fsS -m 2 -o /dev/null "http://127.0.0.1:$port/capabilities" 2>/dev/null
	fi
}

case "$(uname -s)" in
Darwin)
	# On macOS the engine runs as a LaunchAgent. Then macOS asks for the
	# Screen Recording and Accessibility permissions for relay-engine
	# itself. Started over ssh, macOS would ask for the ssh server instead.
	dir="$HOME/.local/state/srterm"
	label="dev.srterm.relay-engine"
	uid=$(id -u)
	engine=""
	for e in "$HOME"/.local/share/screen-relay/engine/*/relay-engine; do
		[ -x "$e" ] && engine=$e # the glob sorts, so the last is the newest version
	done
	if [ ! -x "$engine" ]; then
		echo "error=screen-relay is not installed on this Mac. On the Mac, run: npm i -g screen-relay, then screen-relay install"
		exit 0
	fi
	viewer="$(npm root -g 2>/dev/null)/screen-relay/dist/viewer"
	mkdir -p "$dir" && chmod 700 "$dir"
	if [ ! -s "$dir/token" ]; then
		(umask 077 && openssl rand -hex 32 >"$dir/token")
	fi
	token=$(cat "$dir/token")
	plist="$dir/$label.plist"
	cat >"$plist.new" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>$label</string>
<key>ProgramArguments</key><array>
<string>$engine</string>
<string>--backend</string><string>macos</string>
<string>--bind</string><string>127.0.0.1</string>
<string>--port</string><string>$port</string>
<string>--viewer-dir</string><string>$viewer</string>
<string>--token-file</string><string>$dir/token</string>
</array>
<key>WorkingDirectory</key><string>$(dirname "$engine")</string>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><false/>
<key>ProcessType</key><string>Interactive</string>
<key>StandardOutPath</key><string>$dir/engine.log</string>
<key>StandardErrorPath</key><string>$dir/engine.log</string>
</dict></plist>
EOF
	if ! cmp -s "$plist" "$plist.new"; then
		mv "$plist.new" "$plist"
		launchctl bootout "gui/$uid/$label" 2>/dev/null
	else
		rm -f "$plist.new"
	fi
	if ! ready "$token"; then
		if launchctl print "gui/$uid/$label" >/dev/null 2>&1; then
			launchctl kickstart "gui/$uid/$label"
		else
			launchctl bootstrap "gui/$uid" "$plist"
		fi
		i=0
		while [ $i -lt 20 ] && ! ready "$token"; do
			sleep 0.25
			i=$((i + 1))
		done
	fi
	if ! ready "$token"; then
		log=$(tail -n 4 "$dir/engine.log" 2>/dev/null | tr '\n' ' ')
		case "$log" in
		*permission*)
			echo "error=The Mac has not given relay-engine the Screen Recording and Accessibility permissions. On the Mac, open System Settings, Privacy & Security, and turn on relay-engine in Screen & System Audio Recording and in Accessibility." ;;
		*)
			echo "error=The engine did not start: $log" ;;
		esac
		exit 0
	fi
	echo "port=$port"
	echo "token=$token"
	;;
*)
	if ! command -v screen-relay >/dev/null 2>&1; then
		echo "error=screen-relay is not installed on this host. On the host, run: npm i -g screen-relay, then screen-relay install"
		exit 0
	fi
	if ! ready ""; then
		if ! out=$(screen-relay up -d 2>&1); then
			echo "error=screen-relay up failed: $(printf '%s' "$out" | tail -n 3 | tr '\n' ' ')"
			exit 0
		fi
	fi
	echo "port=$port"
	tf="${XDG_STATE_HOME:-$HOME/.local/state}/screen-relay/default/token"
	if [ -s "$tf" ]; then
		echo "token=$(cat "$tf")"
	fi
	;;
esac
