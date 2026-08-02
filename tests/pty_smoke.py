#!/usr/bin/env python3
"""PTY smoke test for the Agents-tab focus, reattach, and kill controls."""

import json
import fcntl
import os
import pathlib
import pty
import select
import signal
import subprocess
import struct
import sys
import tempfile
import termios
import time

import pyte


def wait_for(fd, stream, screen, needle, timeout=5):
    deadline = time.monotonic() + timeout
    raw = bytearray()
    answered_osc = False
    answered_dsr = False
    while time.monotonic() < deadline:
        ready, _, _ = select.select([fd], [], [], 0.1)
        if ready:
            try:
                data = os.read(fd, 65536)
            except OSError:
                data = b""
            if not data:
                break
            raw.extend(data)
            if not answered_osc and b"\x1b]11;?\x1b\\" in raw:
                os.write(fd, b"\x1b]11;rgb:0000/0000/0000\x07")
                answered_osc = True
            if not answered_dsr and b"\x1b[6n" in raw:
                os.write(fd, b"\x1b[1;1R")
                answered_dsr = True
            # Bubble Tea can render and clear the alternate screen in one read
            # when it exits early. Feed incrementally so transient real screens
            # remain observable instead of only checking the final clear.
            for char in data.decode(errors="replace"):
                stream.feed(char)
                rendered = "\n".join(screen.display)
                if needle in rendered:
                    return rendered
        rendered = "\n".join(screen.display)
        if needle in rendered:
            return rendered
    raise AssertionError(f"screen never contained {needle!r}:\n{rendered}\nraw={bytes(raw)!r}")


def record(path, ident, bead, pane, pid):
    path.write_text(json.dumps({
        "id": ident, "bead_id": bead, "tool": "claude", "mode": "coding",
        "pid": pid, "session_id": "session-" + ident, "cwd": str(path.parent),
        "pane_id": pane, "source": "external", "started_at": "2026-08-02T12:00:00Z",
    }))


def main():
    binary = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else "./beadsboard").resolve()
    with tempfile.TemporaryDirectory(prefix="beadsboard-pty-") as raw:
        root = pathlib.Path(raw)
        (root / ".beads").mkdir()
        agents = root / ".beadsboard" / "agents"
        agents.mkdir(parents=True)
        bindir = root / "bin"
        bindir.mkdir()
        fake_bd = bindir / "bd"
        fake_bd.write_text("#!/bin/sh\ncase \"$1\" in\nexport) printf '%s\\n' '{\"id\":\"epic-1\",\"title\":\"PTY epic\",\"status\":\"open\",\"issue_type\":\"epic\"}' ;;\nready) printf '[]\\n' ;;\ncomments) printf '[]\\n' ;;\nesac\n")
        fake_bd.chmod(0o755)

        sleepers = [subprocess.Popen(["sleep", "30"]) for _ in range(2)]
        record(agents / "agent-a.json", "agent-a", "epic-1", "terminal_7", sleepers[0].pid)
        record(agents / "agent-b.json", "agent-b", "epic-1", "terminal_8", sleepers[1].pid)

        pid, fd = pty.fork()
        if pid == 0:
            env = os.environ.copy()
            env["PATH"] = str(bindir) + os.pathsep + env["PATH"]
            env["TERM"] = "xterm-256color"
            env.pop("ZELLIJ", None)  # Enter reports which recorded pane it would reattach.
            os.execve(str(binary), [str(binary), "--source", str(root)], env)

        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 120, 0, 0))
        os.kill(pid, signal.SIGWINCH)
        screen = pyte.Screen(120, 32)
        stream = pyte.Stream(screen)
        try:
            wait_for(fd, stream, screen, "PTY epic")
            os.write(fd, b"A")
            wait_for(fd, stream, screen, "session session-agent-a")

            os.write(fd, b"\x1b[B")  # focus the second agent
            time.sleep(0.1)
            os.write(fd, b"k")
            deadline = time.monotonic() + 5
            while (agents / "agent-b.json").exists() and time.monotonic() < deadline:
                time.sleep(0.05)
            assert not (agents / "agent-b.json").exists(), "k did not remove the focused registry record"

            # Registry refresh reclamps focus to the remaining row. Enter now
            # exercises that live record's reattach route.
            time.sleep(0.2)
            os.write(fd, b"\r")
            focused = wait_for(fd, stream, screen, "not in zellij")
            assert "session-agent-a" in focused, "remaining agent was not focused for reattach"
        finally:
            try:
                os.kill(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            for proc in sleepers:
                if proc.poll() is None:
                    proc.kill()
                proc.wait()
    print("PTY agent-control smoke test passed")


if __name__ == "__main__":
    main()
