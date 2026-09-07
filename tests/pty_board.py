#!/usr/bin/env python3
"""Exercise board navigation and refresh in a real terminal, using local fixtures."""

import fcntl
import json
import os
import pathlib
import pty
import select
import signal
import struct
import sys
import tempfile
import termios

import pyte
from pty_smoke import wait_for


def main():
    binary = pathlib.Path(sys.argv[1]).resolve()
    with tempfile.TemporaryDirectory(prefix="beadsboard-board-pty-") as tmp:
        root = pathlib.Path(tmp)
        (root / ".beads").mkdir()
        (root / ".beadsboard").mkdir()
        (root / ".beadsboard/config.toml").write_text("github_sync = false\n")
        bindir = root / "bin"
        bindir.mkdir()
        issues = [
            {
                "id": "demo",
                "title": "Release controls",
                "issue_type": "epic",
                "status": "open",
            },
            {
                "id": "demo.1",
                "title": "Completed export",
                "issue_type": "task",
                "status": "closed",
                "priority": 1,
            },
            {
                "id": "demo.2",
                "title": "Queued importer",
                "issue_type": "task",
                "status": "open",
                "priority": 2,
            },
            {
                "id": "demo.3",
                "title": "Active migration",
                "issue_type": "task",
                "status": "in_progress",
                "priority": 2,
                "description": "Review the migration output before releasing.\n" * 35,
            },
            {
                "id": "loose",
                "title": "Orphan approval",
                "issue_type": "bug",
                "status": "blocked",
                "priority": 0,
            },
        ]
        data = root / "issues.jsonl"

        def save():
            data.write_text("\n".join(json.dumps(x) for x in issues) + "\n")

        save()
        (bindir / "bd").write_text(
            '#!/bin/sh\ncase "$1" in\nexport) /bin/cat "$BOARD_FIXTURE" ;;\n*) printf "[]\\n" ;;\nesac\n'
        )
        (bindir / "bd").chmod(0o755)
        (bindir / "codex").write_text("""#!/bin/sh
read -r request
printf '%s\n' '{"id":1,"result":{}}'
read -r request
read -r request
printf '%s\n' '{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":17,"windowDurationMins":300,"resetsAt":2000000000},"secondary":{"usedPercent":37,"windowDurationMins":10080,"resetsAt":2000300000}}}}'
""")
        (bindir / "codex").chmod(0o755)
        pid, fd = pty.fork()
        if pid == 0:
            env = os.environ.copy()
            env.update(
                PATH=str(bindir) + os.pathsep + env["PATH"],
                TERM="xterm-256color",
                BOARD_FIXTURE=str(data),
                CLAUDE_CONFIG_DIR=str(root / "no-login"),
            )
            env.pop("CLAUDE_CODE_OAUTH_TOKEN", None)
            os.execve(str(binary), [str(binary), "--source", str(root)], env)
        screen = pyte.Screen(120, 32)
        stream = pyte.Stream(screen)

        def resize(cols, rows):
            screen.resize(rows, cols)
            fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
            os.kill(pid, signal.SIGWINCH)

        def send(keys, needle):
            os.write(fd, keys)
            return wait_for(fd, stream, screen, needle)

        def capture(name):
            # A render may span several reads. Capture only after the complete
            # frame, rather than at the first appearance of an assertion label.
            while select.select([fd], [], [], 0.2)[0]:
                data = os.read(fd, 65536)
                if not data:
                    break
                stream.feed(stream._beadsboard_decoder.decode(data))
            if len(sys.argv) > 2:
                out = pathlib.Path(sys.argv[2])
                out.mkdir(parents=True, exist_ok=True)
                (out / (name + ".txt")).write_text("\n".join(screen.display))
                try:
                    from PIL import Image, ImageDraw, ImageFont
                except ImportError:
                    return
                font = ImageFont.truetype("/System/Library/Fonts/Menlo.ttc", 14)
                image = Image.new(
                    "RGB", (screen.columns * 9, screen.lines * 19), "#181818"
                )
                draw = ImageDraw.Draw(image)
                palette = {
                    "default": "#dddddd",
                    "black": "#181818",
                    "red": "#cd5555",
                    "green": "#55cd55",
                    "brown": "#cdcd55",
                    "blue": "#5555cd",
                    "magenta": "#cd55cd",
                    "cyan": "#55cdcd",
                    "white": "#dddddd",
                }
                for y, line in screen.buffer.items():
                    for x, cell in line.items():
                        fg = palette.get(cell.fg, "#" + cell.fg)
                        bg = (
                            palette.get(cell.bg, "#" + cell.bg)
                            if cell.bg != "default"
                            else "#181818"
                        )
                        draw.rectangle(
                            (x * 9, y * 19, (x + 1) * 9, (y + 1) * 19), fill=bg
                        )
                        draw.text((x * 9, y * 19), cell.data, font=font, fill=fg)
                image.save(out / (name + ".png"))

        try:
            resize(120, 32)
            wait_for(fd, stream, screen, "Release controls")
            send(b"t", "A all")
            send(b"O", "TASKS (2) open")
            text = wait_for(fd, stream, screen, "17% used")
            assert text.index("Active") < text.index("Queued")
            capture("task-list")
            send(b"/", "search")
            send(b"demo.3", "demo.3")
            send(b"\r", "TASKS (1) open")
            text = send(b"f", "f/esc restore")
            assert "EPICS" not in text
            assert "Active migration" in text
            capture("fullscreen")
            send(b"\x1b", "TASKS (1) open")
            send(b"\x1b", "TASKS (2) open")
            send(b"C", "TASKS (1) closed")
            send(b"i", "ATTENTION")
            text = send(b"\r", "Orphan approval")
            # Drain a full frame after the title appears, then prove the task is open.
            text = wait_for(fd, stream, screen, "tab field")
            assert "title" in text and "Orphan approval" in text
            send(b"v", "Tasks by priority")
            text = wait_for(fd, stream, screen, "Tasks: 4")
            assert "Finished: 1 (25%)" in text
            capture("dashboard-wide")
            issues[1]["status"] = "open"
            save()
            wait_for(fd, stream, screen, "Finished: 0 (0%)", timeout=10)
            resize(80, 24)
            wait_for(fd, stream, screen, "P0: 1 tasks")
            capture("dashboard-compact")
            send(b"\x1b", "tab field")
            send(b"f", "f/esc restore")
            capture("fullscreen-compact")
            os.write(fd, b"q")
        finally:
            try:
                os.kill(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            os.close(fd)
            os.waitpid(pid, 0)
    print("PTY board navigation, limits, resize and live refresh passed")


if __name__ == "__main__":
    main()
