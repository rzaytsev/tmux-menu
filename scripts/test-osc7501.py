#!/usr/bin/env python3
"""Isolated OSC receiver acceptance; uses a synthetic CLI, never a model/API.

Run: python3 scripts/test-osc7501.py [path/to/tmux-menu]
Requires Go, tmux, and PTY/socket access. Never attaches or switches a client.
"""
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import time

FIXTURE = r'''
package main
import (
 "encoding/json"
 "os"
 "strings"
 "github.com/creack/pty"
 "golang.org/x/term"
)
func report(body string) { os.Stdout.WriteString("\x1b]7501;"+body+"\a") }
func metadata(probe bool) {
 size,_:=pty.GetsizeFull(os.Stdin)
 data,_:=json.Marshal(map[string]any{"probe":probe,"rows":size.Rows,"cols":size.Cols})
 os.WriteFile(os.Getenv("OSC_TEST_METADATA"),data,0600)
}
func main(){
 term.MakeRaw(int(os.Stdin.Fd()))
 os.Stdout.WriteString("\x1b]7501;?\a\x1b[c")
 data:=make([]byte,4096); reply:=""
 for !strings.Contains(reply,"\x1b\\") { n,err:=os.Stdin.Read(data);if err!=nil{os.Exit(20)};reply+=string(data[:n]) }
 if !strings.HasPrefix(reply,"\x1b]7501;?:states="){os.Exit(21)}
 metadata(true)
 report("state=working:app=claude-code")
 escape:=0
 for {
  n,err:=os.Stdin.Read(data);if err!=nil{os.Exit(22)}
  for _,key:=range data[:n] {
   if escape==1 { if key=='[' { escape=2 } else { escape=0 }; continue }
   if escape==2 { if key>=64 && key<=126 {escape=0};continue }
   if key==27 {escape=1;continue}
   switch key {
   case 'b':report("state=blocked:kind=permission:app=claude-code")
   case 'q':report("state=blocked:kind=question:app=claude-code")
   case 'i',3:report("state=idle:app=claude-code")
   case 'd':report("state=done:app=claude-code")
   case 'e':report("state=error:app=claude-code")
   case 'w':report("state=working:app=claude-code")
   case 'c':report("state=clear")
   case 'r':os.Stdout.WriteString("\x1bc")
   case 'z':metadata(true)
   case 'h':report("state=blocked:kind=question:id=worker")
   case 'j':report("state=clear:id=worker")
   case 'x':os.Exit(7)
   }
  }
 }
}
'''

def main():
    binary = Path(sys.argv[1] if len(sys.argv) > 1 else "bin/tmux-menu").resolve()
    socket = f"osc7501-test-{os.getpid()}"
    def tmux(*args):
        return subprocess.check_output(["tmux", "-L", socket, *args], text=True).strip()
    def wait_for(check, label):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            value = check()
            if value:
                return value
            time.sleep(0.08)
        raise AssertionError(f"timeout: {label}")
    with tempfile.TemporaryDirectory(prefix="tmux-menu-osc-test-") as directory:
        root = Path(directory)
        (root / "fixture.go").write_text(FIXTURE)
        subprocess.run(["go", "build", "-o", str(root / "claude"), str(root / "fixture.go")], check=True)
        env = os.environ.copy()
        env["TMUX_MENU_AGENT_STATE_DIR"] = str(root / "state")
        env["OSC_TEST_METADATA"] = str(root / "metadata.json")
        env["PATH"] = str(root) + os.pathsep + env["PATH"]
        command = shlex.join(["env", *[f"{name}={env[name]}" for name in ("PATH", "TMUX_MENU_AGENT_STATE_DIR", "OSC_TEST_METADATA")], str(binary), "agent-run", "claude"])
        try:
            pane = tmux("-f", "/dev/null", "new-session", "-d", "-s", "osc", "-x", "100", "-y", "30", "-P", "-F", "#{pane_id}", "/bin/zsh -f")
            (root / "launch.sh").write_text(command + "\n")
            tmux("send-keys", "-t", pane, "-l", "source " + shlex.quote(str(root / "launch.sh")))
            tmux("send-keys", "-t", pane, "Enter")
            tmux("set-window-option", "-t", pane, "remain-on-exit", "on")
            env["TMUX"] = tmux("display-message", "-p", "-t", pane, "#{socket_path},#{pid},0")
            def snapshot():
                data = subprocess.check_output([str(binary), "agent-hook", "snapshot"], env=env, text=True)
                return next((row for row in json.loads(data)["agents"] if row["pane_id"] == pane), {})
            def state(wanted):
                def check():
                    row = snapshot()
                    return row if row.get("status") == wanted and row.get("source") == "osc7501" else None
                return wait_for(check, wanted)
            state("working")
            assert json.loads((root / "metadata.json").read_text())["probe"]
            print("PASS capability reply beats tmux DA; working reaches shared snapshot")
            for key, wanted in (("b", "attention"), ("q", "attention"), ("i", "waiting"), ("d", "completed"), ("e", "attention"), ("w", "working")):
                tmux("send-keys", "-t", pane, "-l", key)
                state(wanted)
            print("PASS working/permission/question/idle/done/error transitions")
            tmux("send-keys", "-t", pane, "-l", "h")
            state("attention")
            tmux("send-keys", "-t", pane, "-l", "j")
            state("working")
            print("PASS child attention and subtree clear")
            tmux("send-keys", "-t", pane, "C-c")
            state("waiting")
            print("PASS keyboard input and Ctrl-C reach child PTY")
            osc_file = next((root / "state" / "osc7501").glob("*.json"))
            wrapper_pid = json.loads(osc_file.read_text())["wrapper_pid"]
            tmux("send-keys", "-t", pane, "C-z")
            wait_for(lambda: "T" in subprocess.check_output(["ps", "-p", str(wrapper_pid), "-o", "state="], text=True), "wrapper suspended")
            wait_for(lambda: snapshot().get("source") != "osc7501", "suspended native evidence hidden")
            tmux("send-keys", "-t", pane, "-l", "fg")
            tmux("send-keys", "-t", pane, "Enter")
            wait_for(lambda: "T" not in subprocess.check_output(["ps", "-p", str(wrapper_pid), "-o", "state="], text=True), "wrapper resumed")
            tmux("send-keys", "-t", pane, "-l", "w")
            state("working")
            print("PASS Ctrl-Z suspends wrapper/child; resume restores input and native status")
            tmux("resize-window", "-t", pane, "-x", "120", "-y", "40")
            def resized():
                tmux("send-keys", "-t", pane, "-l", "z")
                size = json.loads((root / "metadata.json").read_text())
                cols, rows = map(int, tmux("display-message", "-p", "-t", pane, "#{pane_width} #{pane_height}").split())
                return size["cols"] == cols and size["rows"] == rows and cols == 120
            wait_for(resized, "PTY resize")
            print("PASS window resize reaches child PTY")
            for key in ("c", "r"):
                tmux("send-keys", "-t", pane, "-l", "w")
                state("working")
                tmux("send-keys", "-t", pane, "-l", key)
                wait_for(lambda: snapshot().get("source") != "osc7501", "clear/reset fallback")
            print("PASS clear and terminal reset restore fallback evidence")
            tmux("send-keys", "-t", pane, "-l", "x")
            wait_for(lambda: not snapshot(), "dead agent removed")
            exit_file = root / "exit-status"
            tmux("send-keys", "-t", pane, "-l", "printf '%s' $? > " + shlex.quote(str(exit_file)))
            tmux("send-keys", "-t", pane, "Enter")
            wait_for(lambda: exit_file.exists() and exit_file.read_text() == "7", "exit status 7 and restored shell input")
            print("PASS exit status 7 preserved; dead agent status disappears")
        except Exception:
            print(tmux("capture-pane", "-p", "-t", pane)[-500:])
            print("fixture metadata:", (root / "metadata.json").read_text() if (root / "metadata.json").exists() else "missing")
            print("OSC files:", list((root / "state").rglob("*.json")))
            print("snapshot:", snapshot())
            for f in (root / "state").rglob("*.json"): print(f.read_text())

            raise
        finally:
            subprocess.run(["tmux", "-L", socket, "kill-server"], check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

if __name__ == "__main__":
    main()
