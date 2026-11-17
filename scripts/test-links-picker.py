#!/usr/bin/env python3
"""Real popup acceptance with disposable tmux, Neovim and browser/clipboard stubs.
Run with a Python environment containing pyte; no external URLs are opened.
"""
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shlex
import shutil
import struct
import subprocess
import tempfile
import termios
import time
import pyte


def main():
    binary = str(Path('bin/tmux-menu').resolve())
    nvim = shutil.which('nvim')
    tmux = [shutil.which('tmux'), '-L', f'links-picker-{os.getpid()}', '-f', '/dev/null']
    with tempfile.TemporaryDirectory(prefix='tml-', dir='/tmp') as tmp:
        root = Path(tmp).resolve()
        workspace = root / 'workspace'
        workspace.mkdir()
        directory = workspace / 'directory space'
        directory.mkdir()
        (directory / 'directory-preview-marker').write_text('fixture')
        file = workspace / "design notes 'literal.md"
        file.write_text(''.join(f'fixture line {i}\n' for i in range(1, 30)))
        (workspace / 'links.md').write_text(
            '[Unicode 界面 title](https://example.com/a(b)?x=1&y=2#frag)\n'
            '[Unrelated title](https://github.com/o/r/pull/12)\n'
            '[Another title](https://github.com/o/r/actions/runs/1)\n'
            '[https://github.com fake title](https://example.org)\n'
            '[Slack](slack://channel?id=fixture)\n'
            '[Range](<design notes \'literal.md#L5-L10>)\n'
            '[Directory](<directory space/>)\n')
        (workspace / '.tmux-menu.conf').write_text(
            'browser="Fixture Browser"\n[bookmarks]\ndirs=["."]\n'
            '[popup]\nwidth="100%"\nheight="100%"\n')
        helper = root / 'bin'
        helper.mkdir()
        for name, script in {
            'open': '#!/bin/sh\nprintf "%s\\n" "$@" > "$TEST_ROOT/browser-args"\n',
            'pbcopy': '#!/bin/sh\ncat > "$TEST_ROOT/copied"\n',
            'nvim': '#!/bin/sh\nprintf "%s\\n" "$@" > "$TEST_ROOT/editor-args"\nexec ' + shlex.quote(nvim) + ' --clean --listen "$TEST_ROOT/nvim.sock" "$@"\n',
        }.items():
            path = helper / name
            path.write_text(script)
            path.chmod(0o700)
        env = dict(os.environ, HOME=tmp, TERM='xterm-256color', TEST_ROOT=tmp,
                   PATH=str(helper) + ':' + os.environ['PATH'], FZF_DEFAULT_OPTS='', FZF_DEFAULT_OPTS_FILE='')
        env.pop('TMUX', None)
        env.pop('TMUX_PANE', None)

        def run(*args):
            return subprocess.check_output(tmux + list(args), env=env, text=True).strip()

        client = None
        other_client = None
        other_master = None
        master = None
        try:
            pane = run('new-session', '-d', '-P', '-F', '#{pane_id}', '-s', 'source', '-c', str(workspace), '-x', '150', '-y', '42')
            sid = run('display-message', '-p', '-t', pane, '#{session_id}')
            wid = run('display-message', '-p', '-t', pane, '#{window_id}')
            other = run('new-session', '-d', '-P', '-F', '#{pane_id}', '-s', 'other', '-c', str(root))
            run('set-option', '-g', 'status', 'off')
            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 42, 150, 0, 0))
            client = subprocess.Popen(tmux + ['attach-session', '-t', 'source'], stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True)
            os.close(slave)
            screen = pyte.Screen(150, 42)
            # Ignore tmux's private device-status probe (pyte 0.8 API).
            screen.report_device_status = lambda *args, **kwargs: None
            stream = pyte.ByteStream(screen)

            def pump(duration=0.12):
                end = time.monotonic() + duration
                while time.monotonic() < end:
                    if select.select([master], [], [], 0.025)[0]:
                        stream.feed(os.read(master, 1 << 20))
                return '\n'.join(screen.display)

            def until(predicate, label, timeout=8):
                deadline = time.monotonic() + timeout
                latest = ''
                while time.monotonic() < deadline:
                    latest = pump()
                    if predicate(latest):
                        return latest
                raise AssertionError(label + '\n' + latest)

            def keys(raw):
                os.write(master, raw)
                pump()

            generation = 0

            def popup(mode='bookmarks'):
                nonlocal generation
                generation += 1
                done = root / f'done-{generation}'
                command = shlex.join(['env', 'HOME=' + tmp, 'PATH=' + env['PATH'], 'TEST_ROOT=' + tmp,
                                     'FZF_DEFAULT_OPTS=', 'FZF_DEFAULT_OPTS_FILE=',
                                     'TMUX_MENU_ORIGIN_PANE=' + pane, binary, 'popup', mode])
                run('run-shell', '-b', '-t', pane, command + '; touch ' + shlex.quote(str(done)))
                until(lambda s: mode + '> ' in s, 'popup missing')
                return done

            def closed(done):
                until(lambda s: done.exists(), 'popup did not close')

            pump()
            # A newer attached client in another session must not receive this popup.
            other_master, other_slave = pty.openpty()
            fcntl.ioctl(other_slave, termios.TIOCSWINSZ, struct.pack('HHHH', 42, 150, 0, 0))
            other_client = subprocess.Popen(tmux + ['attach-session', '-t', 'other'], stdin=other_slave, stdout=other_slave, stderr=other_slave, env=env, start_new_session=True)
            os.close(other_slave)
            until(lambda s: len(run('list-clients', '-F', '#{session_name}').splitlines()) == 2, 'second client attach')
            done = popup()
            initial = until(lambda s: 'Unrelated title' in s and 'Directory' in s, 'missing initial rows')
            Path('/tmp/tmux-menu-links-rendered.txt').write_text(initial)
            rows = [line for line in screen.display if ' | ' in line and ('https://' in line or 'slack://' in line)]
            # Inspect actual terminal cell positions, not Python character lengths.
            positions = []
            for y, line in enumerate(screen.display):
                if line in rows:
                    positions.append([x for x, cell in screen.buffer[y].items() if cell.data == '|'])
            assert len(positions) >= 4 and all(p == positions[0] for p in positions), positions
            keys(b'\x1bg')
            until(lambda s: 'Unrelated title' in s and 'Another title' in s and 'example.com' not in s and 'fake title' not in s, 'GitHub filter')
            keys(b'\x1bs')
            until(lambda s: 'slack://' in s and 'github.com/o/r' not in s, 'Slack filter')
            keys(b'\x1bf')
            until(lambda s: 'Range' in s and 'slack://' not in s, 'file filter')
            keys(b'\x1f')  # Ctrl+/
            until(lambda s: 'fixture line 5' in s, 'file preview')
            keys(b'\x1f')
            keys(b'\x1bd')
            until(lambda s: 'Directory' in s and 'Range' not in s, 'directory filter')
            keys(b'\x1f')
            until(lambda s: 'directory-preview-marker' in s, 'directory preview')
            keys(b'\x1f')
            keys(b'\x1ba')
            until(lambda s: 'Range' in s and 'Unrelated title' in s, 'restore all')
            keys(b'\x1bg')
            until(lambda s: 'Another title' in s and 'Range' not in s, 'GitHub filter before multi')
            keys(b'\t\t\x19')
            closed(done)
            copied = (root / 'copied').read_text().splitlines()
            assert set(copied) == {'https://github.com/o/r/pull/12', 'https://github.com/o/r/actions/runs/1'}, copied

            done = popup()
            keys(b'\x1bf')
            until(lambda s: 'Range' in s and 'Unrelated title' not in s, 'file filter before Enter')
            keys(b'\r')
            closed(done)
            until(lambda s: (root / 'nvim.sock').exists(), 'Neovim not started')
            expression = "json_encode([expand('%:p'),line('.'),line('v'),mode()])"
            result = json.loads(subprocess.check_output([nvim, '--server', str(root / 'nvim.sock'), '--remote-expr', expression], env=env, text=True))
            assert result[0] == str(file) and {result[1], result[2]} == {5, 10} and result[3] == 'V', result
            editor_pane = run('display-message', '-p', '-t', sid, '#{pane_id}')
            assert editor_pane != pane
            assert run('display-message', '-p', '-t', editor_pane, '#{window_id}') == wid
            subprocess.run([nvim, '--server', str(root / 'nvim.sock'), '--remote-send', '<Esc>:qa!<CR>'], env=env, check=True)
            until(lambda s: run('display-message', '-p', '-t', sid, '#{pane_id}') == pane, 'focus after editor exit')

            done = popup()
            keys(b'\x1bd')
            until(lambda s: 'Directory' in s and 'Range' not in s, 'directory filter before Enter')
            keys(b'\r')
            closed(done)
            destination = run('display-message', '-p', '-t', sid, '#{pane_id}')
            assert destination != pane
            assert run('display-message', '-p', '-t', destination, '#{pane_current_path}') == str(directory)
            assert run('display-message', '-p', '-t', destination, '#{session_id}') == sid
            assert run('display-message', '-p', '-t', destination, '#{pane_current_command}') in {'sh', 'zsh', 'bash', 'fish'}
            assert 'source' in run('list-clients', '-F', '#{session_name}')
            run('kill-window', '-t', run('display-message', '-p', '-t', destination, '#{window_id}'))

            # Dispatch stays anchored even with another session selected at the server.
            run('select-window', '-t', run('display-message', '-p', '-t', other, '#{window_id}'))
            done = popup()
            keys(b'Unicode\r')
            closed(done)
            args = (root / 'browser-args').read_text().splitlines()
            assert args == ['-a', 'Fixture Browser', '--', 'https://example.com/a(b)?x=1&y=2#frag'], args

            # Scrollback uses the source cwd and the same typed actions.
            run('send-keys', '-l', '-t', pane, "printf '%s\\n' " + shlex.quote(str(file) + ':5:12') + ' https://github.com/o/r/issues/3')
            run('send-keys', '-t', pane, 'Enter')
            pump()
            done = popup('links')
            keys(b'\x1bf')
            until(lambda s: ':5:12' in s and 'https://' not in s, 'scrollback files')
            keys(b'\x19')
            closed(done)
            assert (root / 'copied').read_text() == str(file) + ':5:12'
            done = popup('links')
            keys(b'\x1bf')
            until(lambda s: ':5:12' in s and 'https://' not in s, 'column file filter')
            keys(b'\r')
            closed(done)
            until(lambda s: (root / 'nvim.sock').exists(), 'column Neovim start')
            cursor = json.loads(subprocess.check_output([nvim, '--server', str(root / 'nvim.sock'), '--remote-expr', "json_encode([line('.'),col('.')])"], env=env, text=True))
            assert cursor == [5, 12], cursor
            subprocess.run([nvim, '--server', str(root / 'nvim.sock'), '--remote-send', '<Esc>:qa!<CR>'], env=env, check=True)
            print('PASS: real popup, Unicode columns, all filters, preview, multiselect copy, real Neovim range/focus, directory shell/window/focus, stub browser, scrollback cwd')
            print('Rendered terminal: /tmp/tmux-menu-links-rendered.txt')
        finally:
            if other_client:
                other_client.terminate()
                other_client.wait(timeout=5)
            if other_master is not None:
                os.close(other_master)
            if client:
                client.terminate()
                client.wait(timeout=5)
            if master is not None:
                os.close(master)
            subprocess.run(tmux + ['kill-server'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            time.sleep(0.2)


if __name__ == '__main__':
    main()
