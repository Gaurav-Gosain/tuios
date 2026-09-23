"""Hermes plugin installed by tuios to report the session a pane runs."""

# installed by tuios
# managed by tuios; `tuios integration install hermes` overwrites this file and
# `tuios integration uninstall hermes` removes it.
# TUIOS_INTEGRATION_ID=hermes
# TUIOS_INTEGRATION_VERSION=__TUIOS_VERSION__
#
# Reports the session id of an interactive Hermes session to the tuios pane it
# runs in, through `tuios agent-hook hermes`, so the pane can be resumed. The
# pane's state is left to its screen rules.

from __future__ import annotations

import json
import os
import subprocess

_TUIOS = __TUIOS_COMMAND__
_INTERACTIVE = {"cli", "tui", "desktop", "acp"}


def _enabled() -> bool:
    return os.environ.get("TUIOS_ENV") == "1" or bool(os.environ.get("TUIOS_AGENT"))


def _report(event: str, **kwargs) -> None:
    if not _enabled() or kwargs.get("platform") not in _INTERACTIVE:
        return
    session_id = kwargs.get("session_id")
    if not isinstance(session_id, str) or not session_id:
        return
    payload = json.dumps({"hook_event_name": event, "session_id": session_id})
    try:
        options = {
            "input": payload.encode(),
            "stdout": subprocess.DEVNULL,
            "stderr": subprocess.DEVNULL,
            "timeout": 2,
            "check": False,
        }
        if os.name == "nt":
            options["creationflags"] = subprocess.CREATE_NO_WINDOW
        subprocess.run([_TUIOS, "agent-hook", "hermes", "--integration", "__TUIOS_VERSION__"], **options)
    except Exception:
        # A report that cannot be sent must never break Hermes.
        pass


def _session_started(**kwargs) -> None:
    _report("on_session_start", **kwargs)


def _session_reset(**kwargs) -> None:
    _report("on_session_reset", **kwargs)


def register(ctx):
    ctx.register_hook("on_session_start", _session_started)
    ctx.register_hook("on_session_reset", _session_reset)
