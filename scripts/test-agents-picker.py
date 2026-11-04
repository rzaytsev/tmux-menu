#!/usr/bin/env python3
"""Exercise real fzf rendering in an isolated tmux server, without provider calls."""

import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/tmux-menu")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    tmux = [shutil.which("tmux"), "-L", f"agent-picker-test-{os.getpid()}", "-f", "/dev/null"]
    if not tmux[0] or not shutil.which("fzf"):
        raise SystemExit("tmux and fzf are required")

    def run(*command):
        return subprocess.check_output(tmux + list(command), text=True).strip()

    with tempfile.TemporaryDirectory(prefix="agent-picker-test-") as directory:
        root = Path(directory)
        (root / ".tmux-menu.conf").write_text('[session]\ncolor = "auto"\n[popup]\nwidth = "100%"\n')
        helpers = root / "bin"
        helpers.mkdir()
        (helpers / "pbcopy").write_text('#!/bin/sh\ncat > "$AGENT_PICKER_TEST_COPY"\n')
        (helpers / "pbcopy").chmod(0o755)
        copy_path = root / "copied-path"
        panes = []
        try:
            for session, title in [
                ("alpha", "codex|Review infrastructure|Working"),
                ("documentation", "codex|Write " + "operator guide " * 12 + "unique-tail|Ready"),
                ("permissions", "Claude Code permission required"),
                ("ephemeral", "Claude Code permission required"),
            ]:
                workdir = root / session
                workdir.mkdir()
                pane = run("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session,
                           "-x", "160", "-y", "45", "-c", str(workdir),
                           "printf 'Selected pane preview fixture\\nSecond scrollback line\\n'; exec sleep 180")
                run("select-pane", "-t", pane, "-T", title)
                panes.append(pane)
            subprocess.run(["git", "init", "--quiet", "--initial-branch=agent-preview", str(root / "alpha")], check=True)
            command = " ".join(shlex.quote(value) for value in [
                "env", "HOME=" + str(root), "FZF_DEFAULT_OPTS=", "FZF_DEFAULT_OPTS_FILE=",
                "PATH=" + str(helpers) + os.pathsep + os.environ["PATH"],
                "AGENT_PICKER_TEST_COPY=" + str(copy_path),
                "TMUX_MENU_DISPATCH_FILE=" + str(root / "dispatch.json"),
                "TMUX_MENU_ORIGIN_PANE=" + panes[0], "TMUX_MENU_ORIGIN_PATH=" + str(root / "alpha"),
                binary, "agents",
            ])
            picker = run("new-window", "-P", "-F", "#{pane_id}", "-t", "alpha:", "-n", "picker-test", command)

            def screen():
                return run("capture-pane", "-p", "-t", picker)

            def await_text(text, timeout=7):
                deadline = time.monotonic() + timeout
                latest = ""
                while time.monotonic() < deadline:
                    latest = screen()
                    if text in latest:
                        return latest
                    time.sleep(0.05)
                raise AssertionError(f"Missing {text!r}:\n{latest}")

            def key(value):
                run("send-keys", "-t", picker, value)

            def query(value):
                key("C-u")
                run("send-keys", "-l", "-t", picker, value)
                await_text("agents> " + value)

            initial = await_text("Selected pane preview fixture")
            assert all(word in initial for word in ["STATUS", "AGE", "REASON", "WORKDIR"]), initial
            key("Down")
            key("Down")
            await_text("Git: agent-preview")
            run("kill-pane", "-t", panes[3])
            await_text("3 live")
            time.sleep(2.2)
            assert "Git: agent-preview" in screen(), "A vanished preceding row changed selection:\n" + screen()
            before = next(i for i, line in enumerate(screen().splitlines()) if "codex" in line and "alpha" in line)
            run("select-pane", "-t", panes[0], "-T", "codex|Review infrastructure|Ready")
            changed = await_text("0 working")
            after = next(i for i, line in enumerate(changed.splitlines()) if "codex" in line and "alpha" in line)
            assert before == after and "Git: agent-preview" in changed, changed

            # Query matching searches the title suffix omitted from the list.
            query("unique-tail")
            filtered = await_text("1 shown / 3 live")
            await_text("Thread: Write")
            time.sleep(2.2)
            assert "agents> unique-tail" in screen(), screen()
            key("C-r")
            await_text("agents> unique-tail")
            await_text("1 shown / 3 live")
            await_text("Thread: Write")
            key("M-3")
            await_text("tools>")
            key("M-2")
            restored = await_text("agents> unique-tail")
            await_text("Thread: Write")

            key("C-s")
            await_text("No matching agents")
            key("C-s")
            await_text("1 shown / 3 live")
            await_text("[all]")
            key("C-d")
            await_text("No matching agents")
            await_text("[completed]")
            key("C-d")
            await_text("1 shown / 3 live")
            await_text("[all]")
            key("C-a")
            await_text("No matching agents")
            await_text("[attention]")
            run("select-pane", "-t", panes[1], "-T", "codex|Write unique-tail|Action Required")
            await_text("1 shown / 3 live")
            await_text("Thread: Write unique-tail")
            assert "agents> unique-tail" in screen()
            key("C-a")
            await_text("[all]")

            # Resizing updates columns without clearing the query.
            run("resize-window", "-t", "alpha:picker-test", "-x", "80", "-y", "30")
            narrow = await_text("WORKDIR")
            time.sleep(0.2)
            narrow = screen()
            assert "agents> unique-tail" in narrow and "Thread: Write unique-tail" in narrow, narrow
            assert all(len(line) <= 80 for line in narrow.splitlines()), narrow

            # Copy through a fixture pbcopy; the user's clipboard is untouched.
            key("C-y")
            deadline = time.monotonic() + 4
            dispatch_path = root / "dispatch.json"
            while not dispatch_path.exists() and time.monotonic() < deadline:
                time.sleep(0.05)
            if not dispatch_path.exists():
                raise AssertionError("Copy did not dispatch:\n" + screen())
            dispatch = json.loads(dispatch_path.read_text())
            assert dispatch["mode"] == "shell", dispatch
            env = dict(os.environ, PATH=str(helpers) + os.pathsep + os.environ["PATH"], AGENT_PICKER_TEST_COPY=str(copy_path))
            subprocess.run(["/bin/sh", "-c", dispatch["cmd"]], env=env, check=True)
            assert copy_path.read_text() == str((root / "documentation").resolve())
            picker = run("new-window", "-P", "-F", "#{pane_id}", "-t", "alpha:", "-n", "cancel-test", command)
            await_text("Selected pane preview fixture")
            query("no-such-agent")
            await_text("No matching agents")
            key("Escape")
            time.sleep(0.2)
            assert picker not in run("list-panes", "-a", "-F", "#{pane_id}").splitlines()
            print("PASS: live rows keep order/selection; full-title search; rapid typing; query across refresh/views; filters; Git details; resize; copy workdir; empty-list cancellation")
        finally:
            subprocess.run(tmux + ["kill-server"], capture_output=True)


if __name__ == "__main__":
    main()
