#!/usr/bin/env python3
"""Measures tuios-gpui against a private daemon in a headless compositor.

    scripts/measure.py TUIOS BIN OUT.json

TUIOS is the bridge-branch tuios, BIN the tuios-gpui build to measure. The
script seeds a fresh private daemon (scripts/demo/seed.sh plus a "busy4"
session of four streaming panes and a "hist" session with 6,000 lines of
history), then starts BIN in gamescope's headless backend at 1440x900 and
60 Hz, one run per scenario:

  idle-quiet  the demo session, no agent working anywhere
  idle-spin   the demo session, two agents working
  busy4       four panes printing about 200 lines a second each
  busy1       one of three panes printing about 200 lines a second
  scroll      wheel steps of 40 px every 16 ms over the history pane

For each it reads the app's own `stats` control command (frames drawn, grid
paint times, memory) and /proc for CPU. It stops every process it started.

GUI_CONFIG_HOME, when set, is the XDG_CONFIG_HOME the app reads its own
config.toml from (to measure a setting such as gpu = "integrated").
SCENARIOS, when set, is a comma-separated list of the scenarios to run.
"""
import json, os, shutil, signal, socket, subprocess, sys, time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TUIOS, BIN, OUT = (sys.argv + ["", "", ""])[1:4]
WORK = os.path.join(os.environ.get("TMPDIR", "/tmp"), "measure")
BASE = os.path.join(WORK, "env")
HZ = os.sysconf("SC_CLK_TCK")


def env():
    e = dict(os.environ)
    for k, sub in [("XDG_RUNTIME_DIR", "run"), ("XDG_CONFIG_HOME", "config"), ("XDG_STATE_HOME", "state"),
                   ("XDG_CACHE_HOME", "cache"), ("XDG_DATA_HOME", "data"), ("HOME", "home")]:
        e[k] = os.path.join(BASE, sub)
    e.pop("TUIOS_SOCKET", None)
    e["SHELL"] = "/bin/bash"
    return e


def t(*args):
    return subprocess.run([TUIOS, *args], env=env(), capture_output=True, text=True).stdout.strip()


def seed():
    shutil.rmtree(WORK, ignore_errors=True)
    os.makedirs(WORK)
    subprocess.run(["sh", os.path.join(ROOT, "scripts/demo/seed.sh"), TUIOS, BASE],
                   env={**os.environ, "THEME": "tokyonight"}, check=True, capture_output=True)
    busy = os.path.join(ROOT, "scripts/demo/busy.sh")
    t("new", "-d", "busy4", "--cwd", ROOT)
    for i in range(3):
        t("new-window", "-s", "busy4", "--no-focus", "busy", "--", busy)
    first = json.loads(t("ls", "--json"))
    w0 = [s for s in first if s["name"] == "busy4"][0]["windows"][0]["id"]
    t("close-window", "-s", "busy4", w0)
    t("new-window", "-s", "busy4", "--no-focus", "busy", "--", busy)
    t("new", "-d", "busy1", "--cwd", ROOT)
    first = json.loads(t("ls", "--json"))
    w0 = [s for s in first if s["name"] == "busy1"][0]["windows"][0]["id"]
    t("new-window", "-s", "busy1", "--no-focus", "busy", "--", busy)
    t("new-window", "-s", "busy1", "--no-focus", "quiet", "--", "sh", "-c", "seq 40; exec sleep 1d")
    t("new-window", "-s", "busy1", "--no-focus", "quiet", "--", "sh", "-c", "seq 40; exec sleep 1d")
    t("close-window", "-s", "busy1", w0)
    hist = "seq -f 'line %g of the history, with a few words to shape' 6000; exec sleep 1d"
    t("new", "-d", "hist", "--cwd", ROOT)
    t("new-window", "-s", "hist", "--no-focus", "history", "--", "sh", "-c", hist)
    first = json.loads(t("ls", "--json"))
    w0 = [s for s in first if s["name"] == "hist"][0]["windows"][0]["id"]
    t("close-window", "-s", "hist", w0)


def cpu(pid):
    try:
        f = open(f"/proc/{pid}/stat").read().rsplit(")", 1)[1].split()
        return int(f[11]) + int(f[12]), int(f[13]) + int(f[14])
    except OSError:
        return 0, 0


def children(pid):
    out = subprocess.run(["ps", "-o", "pid=", "--ppid", str(pid)], capture_output=True, text=True).stdout
    return [int(x) for x in out.split()]


class Ctl:
    def __init__(self, path):
        for _ in range(100):
            try:
                self.s = socket.socket(socket.AF_UNIX)
                self.s.connect(path)
                break
            except OSError:
                time.sleep(0.1)
        self.f = self.s.makefile("rw")

    def __call__(self, cmd):
        self.f.write(cmd + "\n")
        self.f.flush()
        return self.f.readline().strip()


def gui_env():
    e = dict(os.environ)
    if os.environ.get("GUI_CONFIG_HOME"):
        e["XDG_CONFIG_HOME"] = os.environ["GUI_CONFIG_HOME"]
    return e


def wanted(name):
    only = os.environ.get("SCENARIOS")
    return not only or name in only.split(",")


def run(name, session, seconds, action=None, setup=None):
    sock = os.path.join(WORK, f"{name}.sock")
    gs = subprocess.Popen(
        ["gamescope", "--backend", "headless", "-W", "1440", "-H", "900", "-r", "60", "--expose-wayland", "--",
         BIN, "--tuios", TUIOS, "--isolate", BASE, "--session", session, "--theme", "tokyonight", "--control", sock],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True, env=gui_env())
    try:
        ctl = Ctl(sock)
        if setup:
            setup()
        time.sleep(4)
        pid = json.loads(ctl("stats"))["pid"]
        bridge = [c for c in children(pid)]
        ctl("resetstats")
        a0, c0 = cpu(pid)
        b0 = sum(cpu(b)[0] for b in bridge)
        start = time.time()
        if action:
            action(ctl, seconds)
        else:
            time.sleep(seconds)
        el = time.time() - start
        a1, c1 = cpu(pid)
        b1 = sum(cpu(b)[0] for b in bridge)
        st = json.loads(ctl("stats"))
        return {
            "scenario": name,
            "seconds": round(el, 2),
            "app_cpu_pct": round((a1 - a0) / HZ / el * 100, 2),
            "children_cpu_pct": round((c1 - c0) / HZ / el * 100, 2),
            "bridge_cpu_pct": round((b1 - b0) / HZ / el * 100, 2),
            "frames_per_s": round(st["frames"] / el, 2),
            "grid_paints_per_s": round(st["grid_paints"] / el, 2),
            "paint_p50_ms": st["paint_p50_ms"],
            "paint_p95_ms": st["paint_p95_ms"],
            "rss_mb": round(st["rss_kb"] / 1024, 1),
            "anon_mb": round(st["anon_kb"] / 1024, 1),
        }
    finally:
        os.killpg(gs.pid, signal.SIGTERM)
        try:
            gs.wait(5)
        except subprocess.TimeoutExpired:
            os.killpg(gs.pid, signal.SIGKILL)


def scroll(ctl, seconds):
    end = time.time() + seconds
    up = True
    n = 0
    while time.time() < end:
        ctl(f"wheel 0 {40 if up else -40}")
        n += 1
        if n % 60 == 0:
            up = not up
        time.sleep(0.016)


def quiet():
    """Sets every working agent idle and returns them."""
    rows = json.loads(t("list-agents", "--all", "--all-sessions", "--json"))["agents"]
    working = [a for a in rows if a.get("state") == "working"]
    for a in working:
        t("set-agent-state", "idle", "-s", a["session"], "-w", a["window_id"], "--harness", a.get("harness_id", ""), "-m", a.get("message", ""))
    return working


def spin(working):
    for a in working:
        t("set-agent-state", "working", "-s", a["session"], "-w", a["window_id"], "--harness", a.get("harness_id", ""), "-m", a.get("message", ""))


def main():
    seed()
    results = []
    try:
        working = quiet()
        if wanted("idle-quiet"):
            results.append(run("idle-quiet", "tuios", 15))
        spin(working)
        if wanted("idle-spin"):
            results.append(run("idle-spin", "tuios", 15))
        if wanted("busy4"):
            results.append(run("busy4", "busy4", 15))
        if wanted("busy1"):
            results.append(run("busy1", "busy1", 15))
        if wanted("scroll"):
            results.append(run("scroll", "hist", 8, action=scroll))
    finally:
        t("kill-server")
    for r in results:
        print(json.dumps(r))
    json.dump(results, open(OUT, "w"), indent=1)


if __name__ == "__main__":
    main()
