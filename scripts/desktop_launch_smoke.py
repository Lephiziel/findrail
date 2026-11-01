#!/usr/bin/env python3
"""Exercise the extracted native desktop launcher and single-owner handoff."""
import json
import os
import pathlib
import platform
import subprocess
import sys
import tempfile
import time
import urllib.request
from smoke import pdf_fixture

urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))


def main():
    executable_name = 'findrail-desktop.exe' if os.name == 'nt' else 'findrail-desktop'
    launcher = pathlib.Path(__file__).resolve().parents[1] / executable_name
    # The installed app archive is adjacent to the CLI; callers can pass the
    # extracted CLI path to keep this check pinned to that package directory.
    if len(os.sys.argv) == 2:
        launcher = pathlib.Path(os.sys.argv[1]).resolve().parent / executable_name
    if platform.system() == 'Darwin':
        launcher = launcher.parent / 'Findrail.app/Contents/MacOS/Findrail'
    if not launcher.is_file():
        raise SystemExit(f'packaged desktop launcher missing: {launcher}')
    with tempfile.TemporaryDirectory(prefix='findrail desktop launcher ') as temporary:
        root = pathlib.Path(temporary)
        data = root / 'private data'
        notes = root / 'synthetic notes'
        notes.mkdir()
        (notes / 'desktop.pdf').write_bytes(pdf_fixture(['desktop first page', 'desktopworkermarker page two']))
        adapters = root / 'controlled opener'
        adapters.mkdir()
        log = root / 'opener.log'
        if platform.system() == 'Windows':
            command = 'rundll32.exe'
            source = adapters / 'controlled_opener.go'
            source.write_text('''package main
import ("encoding/json"; "os")
func main() { f, err := os.OpenFile(os.Getenv("FINDRAIL_OPENER_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); if err != nil { os.Exit(2) }; defer f.Close(); _ = json.NewEncoder(f).Encode(append([]string{"rundll32.exe"}, os.Args[1:]...)) }
''', encoding='utf-8')
            subprocess.run(['go', 'build', '-o', str(adapters / command), str(source)], check=True, timeout=120)
        else:
            command = 'open' if platform.system() == 'Darwin' else 'xdg-open'
            adapter = adapters / command
            adapter.write_text(f"#!{sys.executable}\nimport json,os,sys\nwith open(os.environ['FINDRAIL_OPENER_LOG'],'a',encoding='utf-8') as f:f.write(json.dumps([os.path.basename(sys.argv[0]),*sys.argv[1:]])+'\\n')\n", encoding='utf-8')
            adapter.chmod(0o755)
        env = {**os.environ, 'PATH': str(adapters) + os.pathsep + os.environ.get('PATH', ''),
               'FINDRAIL_OPENER_LOG': str(log)}
        first = subprocess.Popen([str(launcher), '--data-dir', str(data), str(notes)], env=env,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            record_path = data / '.desktop-instance.json'
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline and not record_path.exists():
                if first.poll() is not None:
                    raise AssertionError('first desktop launch exited: ' + first.stderr.read())
                time.sleep(.05)
            if not record_path.exists():
                raise AssertionError('desktop owner did not publish its endpoint')
            record = json.loads(record_path.read_text(encoding='utf-8'))
            base = record['url']
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                try:
                    with urllib.request.urlopen(base + '/healthz', timeout=1) as response:
                        if response.status == 200:
                            break
                except OSError:
                    time.sleep(.05)
            else:
                raise AssertionError('desktop endpoint did not become ready')
            with urllib.request.urlopen(base + '/api/v1/search?q=desktopworkermarker', timeout=3) as response:
                evidence = json.load(response)
            if evidence['total'] != 1 or evidence['results'][0].get('page') != 2:
                raise AssertionError(f'packaged launcher PDF worker failed: {evidence!r}')

            second = subprocess.run([str(launcher), '--data-dir', str(data)], env=env,
                                    capture_output=True, text=True, timeout=15)
            if second.returncode:
                raise AssertionError(f'second launch failed: {second.stdout}\n{second.stderr}')
            if not first.poll() is None:
                raise AssertionError('second launch replaced/exited the original desktop owner')
            if json.loads(record_path.read_text(encoding='utf-8')) != record:
                raise AssertionError('second launch changed the owning instance record')
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline and (not log.exists() or len(log.read_text().splitlines()) < 2):
                time.sleep(.05)
            opened = [json.loads(line) for line in log.read_text(encoding='utf-8').splitlines()] if log.exists() else []
            expected = ([command, 'url.dll,FileProtocolHandler', base] if platform.system() == 'Windows'
                        else [command, base])
            if len(opened) != 2 or opened[0] != expected or opened[1] != expected:
                raise AssertionError(f'first/second launch opener adapter mismatch: {opened!r}')

            origin = base
            request = urllib.request.Request(base + '/api/v1/session', headers={'Origin': origin})
            with urllib.request.urlopen(request, timeout=3) as response:
                token = json.load(response)['token']
            request = urllib.request.Request(base + '/api/v1/desktop/quit', data=b'{}', method='POST',
                headers={'Origin': origin, 'Host': base.removeprefix('http://'),
                         'X-Findrail-Token': token, 'Sec-Fetch-Site': 'same-origin',
                         'Content-Type': 'application/json'})
            with urllib.request.urlopen(request, timeout=3) as response:
                if response.status != 202:
                    raise AssertionError('desktop Quit was not accepted')
            first.communicate(timeout=10)
            if first.returncode:
                raise AssertionError('desktop owner did not stop cleanly')
            if record_path.exists():
                raise AssertionError('desktop stop did not clean its instance record')
            print(f'{platform.platform()}: extracted launcher first/second launch reused one verified owner; controlled native executable adapter ran twice; quit drained and cleaned up')
        finally:
            if first.poll() is None:
                first.terminate()
                try:
                    first.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    first.kill(); first.communicate(timeout=3)


if __name__ == '__main__':
    main()
