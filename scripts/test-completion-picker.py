#!/usr/bin/env python3
"""Real popup acceptance against a disposable bracketed-paste draft editor.

Requires tmux, fzf and Python with pyte. Never sends input to a real agent.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import signal
import shlex
import shutil
import struct
import subprocess
import sys
import tempfile
import termios
import time

import pyte


FIXTURE = r'''
import json, os, select, sys, time, tty
from pathlib import Path
root = Path(sys.argv[1])
tty.setraw(0)
os.write(1, b'\x1b[?2004h\r\nDRAFT: before | after\r\nhttps://example.invalid/context\r\n')
state = {'pastes': [], 'submit': 0, 'unexpected': '', 'draft': 'before  after'}
def save():
    tmp = root / 'events.tmp'
    tmp.write_text(json.dumps(state))
    tmp.replace(root / 'events.json')
save()
pending = b''
disabled = False
while True:
    if (root / 'disable').exists() and not disabled:
        os.write(1, b'\x1b[?2004l')
        disabled = True
    if not select.select([0], [], [], .05)[0]:
        continue
    pending += os.read(0, 65536)
    while pending:
        if pending.startswith(b'\x1b[200~'):
            end = pending.find(b'\x1b[201~')
            if end < 0:
                break
            text = pending[6:end].decode('utf-8')
            state['pastes'].append(text)
            state['draft'] = 'before ' + ''.join(state['pastes']) + ' after'
            pending = pending[end+6:]
            os.write(1, ('\r\nDRAFT: ' + state['draft'] + '\r\n').encode())
        elif b'\x1b[200~'.startswith(pending):
            break
        else:
            c, pending = pending[:1], pending[1:]
            if c in (b'\r', b'\n'):
                state['submit'] += 1
            state['unexpected'] += repr(c)
        save()
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', default='bin/tmux-menu')
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    tmux = [shutil.which('tmux'), '-L', f'completion-{os.getpid()}', '-f', '/dev/null']
    with tempfile.TemporaryDirectory(prefix='tm-complete-', dir='/tmp') as tmp:
        root = Path(tmp).resolve()
        workspace = root / 'workspace'
        workspace.mkdir()
        (workspace / 'alpha space.txt').write_text('ALPHA PREVIEW MARKER\n')
        (workspace / 'beta.txt').write_text('BETA PREVIEW MARKER\n')
        (workspace / 'empty dir').mkdir()
        (workspace / '.gitignore').write_text('ignored*\n')
        (workspace / '.tmux-menu.conf').write_text('[popup]\nwidth="100%"\nheight="100%"\n')
        fixture = root / 'input.py'
        fixture.write_text(FIXTURE)
        helpers = root / 'bin'
        helpers.mkdir()
        (helpers / 'pbcopy').write_text('#!/bin/sh\ncat > "$COMPLETION_TEST_ROOT/copied"\n')
        (helpers / 'pbcopy').chmod(0o700)
        env = dict(os.environ, HOME=str(root), TERM='xterm-256color',
                   PATH=str(helpers) + ':' + os.environ['PATH'], COMPLETION_TEST_ROOT=str(root),
                   FZF_DEFAULT_OPTS='', FZF_DEFAULT_OPTS_FILE='')
        for name in ['TMUX', 'TMUX_PANE', 'TMUX_MENU_DISPATCH_FILE', 'TMUX_MENU_PICKER_STATE_FILE',
                     'TMUX_MENU_ORIGIN_PANE', 'TMUX_MENU_ORIGIN_PATH', 'TMUX_MENU_ORIGIN_CLIENT',
                     'TMUX_MENU_SESSION_ID', 'TMUX_MENU_SESSION_NAME', 'TMUX_MENU_SESSION_PATH']:
            env.pop(name, None)

        def git(*args):
            return subprocess.check_output(['git', '-c', 'user.name=Fixture', '-c',
                                           'user.email=fixture@example.invalid', '-c', 'commit.gpgsign=false',
                                           '-C', str(workspace), *args], env=env, text=True).strip()

        git('init', '-q', '-b', 'main')
        git('add', '.')
        git('commit', '-qm', 'Fixture initial commit')
        git('branch', 'feature-context')
        git('tag', 'v-fixture')
        commit = git('rev-parse', 'HEAD')
        (workspace / 'beta.txt').write_text('BETA CHANGED MARKER\n')
        (workspace / 'untracked.go').write_text('new\n')
        (workspace / 'ignored-secret').write_text('ignored\n')

        def run(*args):
            return subprocess.check_output(tmux + list(args), env=env, text=True).strip()

        client, master = None, None
        try:
            pane = run('new-session', '-d', '-P', '-F', '#{pane_id}', '-s', 'source',
                       '-x', '170', '-y', '45', '-c', str(workspace),
                       shlex.join([sys.executable, str(fixture), str(root)]))
            other = run('new-window', '-d', '-P', '-F', '#{pane_id}', '-t', 'source:', '-n', 'other', 'sleep 300')
            run('set-option', '-g', 'status', 'off')
            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 45, 170, 0, 0))
            client = subprocess.Popen(tmux + ['attach-session', '-t', 'source:0'], env=env,
                                      stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
            os.close(slave)
            screen = pyte.Screen(170, 45)
            screen.report_device_status = lambda *a, **k: None
            stream = pyte.ByteStream(screen)

            def pump(duration=.12):
                until = time.monotonic() + duration
                while time.monotonic() < until:
                    if select.select([master], [], [], .025)[0]:
                        stream.feed(os.read(master, 1 << 20))
                return '\n'.join(screen.display)

            def until(predicate, label, timeout=10):
                deadline = time.monotonic() + timeout
                latest = ''
                while time.monotonic() < deadline:
                    latest = pump()
                    if predicate(latest):
                        return latest
                raise AssertionError(label + '\n' + latest)

            def keys(value):
                os.write(master, value)
                pump()

            def state():
                return json.loads((root / 'events.json').read_text())

            generation = 0

            def popup(provider=None):
                nonlocal generation
                generation += 1
                done = root / f'done-{generation}'
                log = root / f'log-{generation}'
                command = shlex.join(['env', 'HOME=' + str(root), 'PATH=' + env['PATH'],
                                     'COMPLETION_TEST_ROOT=' + str(root), 'FZF_DEFAULT_OPTS=',
                                     'FZF_DEFAULT_OPTS_FILE=', 'TMUX_MENU_ORIGIN_PANE=' + pane,
                                     binary, 'popup', 'complete'] + ([provider] if provider else []))
                run('run-shell', '-b', '-t', pane, command + ' >' + shlex.quote(str(log)) + ' 2>&1; touch ' + shlex.quote(str(done)))
                until(lambda s: 'complete> ' in s, 'completion popup missing')
                return done, log

            def closed(done):
                until(lambda s: done.exists(), 'popup did not close')

            until(lambda s: (root / 'events.json').exists() and run('display-message', '-p', '-t', pane, '#{bracket_paste_flag}') == '1', 'draft fixture readiness')
            done, log = popup()
            initial = until(lambda s: 'alpha space.txt' in s and 'untracked.go' in s, 'file listing')
            assert 'ignored-secret' not in initial, initial
            keys(b'alpha space')
            until(lambda s: 'ALPHA PREVIEW MARKER' in s, 'file preview')
            rendered = pump()
            Path('/tmp/tmux-menu-completion-rendered.txt').write_text(rendered)
            # Exercise the responsive bottom preview in a narrower real client.
            screen.resize(lines=32, columns=100)
            fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack('HHHH', 32, 100, 0, 0))
            os.kill(client.pid, signal.SIGWINCH)
            narrow = until(lambda s: 'ALPHA PREVIEW MARKER' in s and 'U screen' in s, 'narrow preview and help')
            Path('/tmp/tmux-menu-completion-narrow-rendered.txt').write_text(narrow)
            screen.resize(lines=45, columns=170)
            fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack('HHHH', 45, 170, 0, 0))
            os.kill(client.pid, signal.SIGWINCH)
            until(lambda s: 'ALPHA PREVIEW MARKER' in s, 'restore wide preview')
            keys(b'\x15.txt')
            until(lambda s: 'beta.txt' in s and 'alpha space.txt' in s, 'multiselect results')
            keys(b'\t\t\r')
            closed(done)
            until(lambda s: len(state()['pastes']) == 1, 'literal paste')
            first = state()['pastes'][0]
            assert first in {'alpha space.txt beta.txt', 'beta.txt alpha space.txt'}, first
            assert state()['draft'] == 'before ' + first + ' after', state()
            assert state()['submit'] == 0 and state()['unexpected'] == '', state()

            done, log = popup('branches')
            until(lambda s: 'feature-context' in s, 'branches')
            keys(b'feature')
            until(lambda s: 'complete> feature' in s and '1/2' in s, 'branch filtering')
            keys(b'\r')
            closed(done)
            until(lambda s: state()['pastes'][-1] == 'feature-context', 'branch text insertion')

            done, log = popup('commits')
            until(lambda s: 'Fixture initial commit' in s, 'commit source')
            keys(b'\x19')
            closed(done)
            assert (root / 'copied').read_text() == commit, (root / 'copied').read_text()
            count = len(state()['pastes'])

            done, log = popup()
            keys(b'beta')
            until(lambda s: 'BETA CHANGED MARKER' in s and '1/5' in s, '@path filtering')
            keys(b'\x1b\r')  # Alt-Enter literal @path text
            closed(done)
            until(lambda s: state()['pastes'][-1] == '@beta.txt', '@path insertion')

            done, log = popup()
            keys(b'alpha')
            until(lambda s: 'ALPHA PREVIEW MARKER' in s and '1/5' in s, 'shell quoting filtering')
            keys(b'\x13')
            closed(done)
            until(lambda s: state()['pastes'][-1] == "'alpha space.txt'", 'explicit shell quoting')

            done, log = popup('dirs')
            until(lambda s: 'empty dir' in s, 'empty directory listing')
            keys(b'\x1bc')
            until(lambda s: 'changed' in s and 'untracked.go' in s, 'provider switch')
            keys(b'beta\x12')
            until(lambda s: 'complete> beta' in s and 'BETA CHANGED MARKER' in s, 'refresh/query/preview')
            keys(b'\x1bu')
            until(lambda s: 'https://example.invalid/context' in s, 'screen references')
            keys(b'\x1b1')
            until(lambda s: 'tmux-menu>' in s, 'leave completion')
            keys(b'\x1b9')
            until(lambda s: 'complete>' in s, 'Alt-9 completion navigation')
            keys(b'\x1b')
            closed(done)
            assert len(state()['pastes']) == count + 2, state()

            # Move the source to another window while selection is open.
            done, log = popup()
            keys(b'beta')
            until(lambda s: 'complete> beta' in s, 'query before origin move')
            run('join-pane', '-d', '-s', pane, '-t', other)
            keys(b'\r')
            closed(done)
            assert 'identity is no longer live' in log.read_text(), log.read_text()
            assert len(state()['pastes']) == count + 2, state()

            # Restore source to a focused window for the disabled-mode check.
            run('break-pane', '-s', pane, '-n', 'source-restored')
            (root / 'disable').touch()
            until(lambda s: run('display-message', '-p', '-t', pane, '#{bracket_paste_flag}') == '0', 'disable paste mode')
            done, log = popup()
            until(lambda s: 'paste unavailable' in s, 'disabled paste mode visible')
            keys(b'beta')
            until(lambda s: 'BETA CHANGED MARKER' in s and '1/5' in s, 'disabled paste filtering')
            keys(b'\r')
            closed(done)
            assert 'bracketed paste' in log.read_text(), log.read_text()
            assert len(state()['pastes']) == count + 2, state()
            done, log = popup()
            keys(b'beta')
            until(lambda s: 'BETA CHANGED MARKER' in s and '1/5' in s, 'copy filtering')
            keys(b'\x19')
            closed(done)
            assert (root / 'copied').read_text() == 'beta.txt'
            assert state()['submit'] == 0 and state()['unexpected'] == '', state()
            print('PASS: real popup; files/ignores/untracked; wide/narrow preview; multiselect; exact draft prefix/suffix; branch paste; full commit copy; @path; shell quoting; provider/Alt-9 navigation; refresh; screen refs; cancel; moved origin; disabled paste; zero submit/edit events')
            print('Rendered terminal: /tmp/tmux-menu-completion-rendered.txt')
        finally:
            if client:
                client.terminate()
                client.wait(timeout=5)
            if master is not None:
                os.close(master)
            subprocess.run(tmux + ['kill-server'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    main()
