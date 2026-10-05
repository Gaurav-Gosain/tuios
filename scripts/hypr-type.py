#!/usr/bin/env python3
"""Types text into one window through Hyprland's send_shortcut dispatcher,
without moving keyboard focus. For driving tuios-gpui in tests.

    scripts/hypr-type.py 'class:dev.tuios.gpui' 'ls -la' --enter
    scripts/hypr-type.py 'class:dev.tuios.gpui' --key 'CTRL SHIFT' d
"""
import subprocess, sys, time

NAMES = {
    " ": "space", "-": "minus", "_": ("SHIFT", "minus"), ".": "period", "/": "slash",
    ",": "comma", ";": "semicolon", ":": ("SHIFT", "semicolon"), "'": "apostrophe",
    '"': ("SHIFT", "apostrophe"), "=": "equal", "+": ("SHIFT", "equal"), "|": ("SHIFT", "backslash"),
    "\\": "backslash", "&": ("SHIFT", "7"), "*": ("SHIFT", "8"), "(": ("SHIFT", "9"),
    ")": ("SHIFT", "0"), "$": ("SHIFT", "4"), "#": ("SHIFT", "3"), "!": ("SHIFT", "1"),
    "@": ("SHIFT", "2"), "%": ("SHIFT", "5"), "^": ("SHIFT", "6"), "~": ("SHIFT", "grave"),
    "`": "grave", "<": ("SHIFT", "comma"), ">": ("SHIFT", "period"), "?": ("SHIFT", "slash"),
    "[": "bracketleft", "]": "bracketright", "{": ("SHIFT", "bracketleft"), "}": ("SHIFT", "bracketright"),
    "\n": "Return", "\t": "Tab",
}

def send(window, mods, key):
    expr = 'hl.dsp.send_shortcut({mods = "%s", key = "%s", window = "%s"})' % (mods, key, window)
    subprocess.run(["hyprctl", "dispatch", expr], check=True, stdout=subprocess.DEVNULL)

def type_text(window, text, delay=0.01):
    for ch in text:
        if ch.isalpha() and ch.isupper():
            send(window, "SHIFT", ch.lower())
        elif ch.isalnum():
            send(window, "", ch)
        else:
            k = NAMES.get(ch)
            if k is None:
                raise SystemExit(f"no key for {ch!r}")
            mods, key = k if isinstance(k, tuple) else ("", k)
            send(window, mods, key)
        time.sleep(delay)

if __name__ == "__main__":
    args = sys.argv[1:]
    window = args.pop(0)
    if args and args[0] == "--key":
        send(window, args[1], args[2])
        sys.exit(0)
    enter = "--enter" in args
    args = [a for a in args if a != "--enter"]
    type_text(window, " ".join(args))
    if enter:
        send(window, "", "Return")
