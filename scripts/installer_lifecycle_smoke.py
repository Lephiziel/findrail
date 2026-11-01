#!/usr/bin/env python3
"""Exercise packaged per-user installer resources in a disposable native destination."""
import argparse
import base64
import hashlib
import json
import os
import pathlib
import platform
import shutil
import signal
import sys
import tempfile
import time

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import package
from candidate_record import CaseRecorder
from packaged_journey import command


def directory_fingerprint(root):
    return {path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted(root.rglob('*')) if path.is_file()}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('archive', type=pathlib.Path)
    parser.add_argument('--candidate-sha', required=True)
    parser.add_argument('--report', type=pathlib.Path)
    args = parser.parse_args()
    archive = args.archive.resolve()
    temp_prefix = 'fr ' if os.name == 'nt' else 'findrail install 📦 '
    with tempfile.TemporaryDirectory(prefix=temp_prefix) as temp:
        root = pathlib.Path(temp)
        expected_target = {'Linux': 'linux-amd64' if platform.machine().lower() in ('x86_64', 'amd64') else 'linux-arm64',
                           'Darwin': 'darwin-amd64' if platform.machine().lower() in ('x86_64', 'amd64') else 'darwin-arm64',
                           'Windows': 'windows-amd64'}[platform.system()]
        package.verify_manifest(archive.parent)
        suffix = f'_{expected_target}' + ('.zip' if archive.suffix == '.zip' else '.tar.gz')
        if not archive.name.startswith('findrail_') or not archive.name.endswith(suffix):
            raise ValueError('archive target/name does not match this native runner')
        version = archive.name[len('findrail_'):-len(suffix)]
        package.verify_archive(archive, expected_target, version)
        # WScript.Shell expands percent-delimited environment references in
        # shortcut targets, so Windows uses only valid Unicode/space path chars.
        first_name = 'é space' if os.name == 'nt' else 'первый путь % "$ \\'
        first = package.extract_verified(archive, root / first_name)
        second_name = 'update extraction' if os.name == 'nt' else 'updated extraction 📦'
        second = package.extract_verified(archive, root / second_name)
        info = json.loads((first / 'BUILD-INFO.json').read_text(encoding='utf-8'))
        if info.get('revision') != args.candidate_sha or info.get('dirty') is not False:
            raise ValueError('verified archive provenance does not match candidate SHA / clean source')
        os_name = platform.system()
        if os_name == 'Linux':
            resource = pathlib.Path('launcher/linux')
            destination = root / 'owned applications'
            destination.mkdir()
            script_name, uninstall_name = 'install-user.sh', 'uninstall-user.sh'
            run = lambda tree, name: command(['sh', str(tree / resource / name), '--destination', str(destination)], timeout=45)
            entry = destination / 'findrail.desktop'
            def inspect(tree):
                text = entry.read_text()
                expected = str(tree / 'findrail-desktop').replace('\\', '\\\\')
                if f'TryExec={expected}\n' not in text or 'X-Findrail-Managed=true' not in text:
                    raise AssertionError('desktop entry target/provenance mismatch')
            inspect_expected = lambda tree: inspect(tree)
        elif os_name == 'Darwin':
            resource = pathlib.Path('launcher/macos')
            destination = root / 'owned Applications'
            destination.mkdir()
            script_name, uninstall_name = 'install-user.sh', 'uninstall-user.sh'
            run = lambda tree, name: command(['sh', str(tree / resource / name), '--destination', str(destination)], timeout=45)
            entry = destination / 'Findrail.app'
            def inspect(tree):
                for relative in ('Contents/MacOS/Findrail', 'Contents/Info.plist'):
                    if (entry / relative).read_bytes() != (tree / 'Findrail.app' / relative).read_bytes():
                        raise AssertionError('app resource/executable provenance mismatch')
            inspect_expected = lambda tree: inspect(tree)
        elif os_name == 'Windows':
            resource = pathlib.Path('launcher/windows')
            destination = root / 'owned Programs'
            destination.mkdir()
            script_name, uninstall_name = 'install-user.ps1', 'uninstall-user.ps1'
            # Windows PowerShell 5.1 runs STA by default, as required by the
            # WScript.Shell shortcut COM server. Fall back to pwsh if absent.
            ps = shutil.which('powershell') or shutil.which('pwsh')
            if not ps:
                raise RuntimeError('PowerShell unavailable on Windows native runner')
            inspect_script = root / 'inspect-shortcut.ps1'
            inspect_script.write_text("param([string]$ShortcutPath)\n$s=(New-Object -ComObject WScript.Shell).CreateShortcut($ShortcutPath)\n$j=[pscustomobject]@{Target=$s.TargetPath;Description=$s.Description}|ConvertTo-Json -Compress\n[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($j))\n", encoding='utf-8')
            def run(tree, name):
                script = tree / resource / name
                argv = [ps, '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', str(script)]
                if name == script_name:
                    argv += ['-Binary', str(tree / 'findrail-desktop.exe'), '-DestinationDirectory', str(destination)]
                else:
                    argv += [str(destination)]
                try:
                    return command(argv, timeout=45)
                except AssertionError as error:
                    detail = str(error)
                    for private_path in (str(root), os.environ.get('USERPROFILE', ''), os.environ.get('RUNNER_TEMP', '')):
                        if private_path:
                            detail = detail.replace(private_path, '<runner-path>')
                    raise RuntimeError('PowerShell installer failed: ' + detail[-2000:]) from None
            entry = destination / 'Findrail.lnk'
            def inspect(tree):
                encoded = command([ps, '-NoProfile', '-File', str(inspect_script), '-ShortcutPath', str(entry)], timeout=30).stdout.strip()
                value = json.loads(base64.b64decode(encoded).decode('utf-8'))
                expected_binary = tree / 'findrail-desktop.exe'
                try:
                    same_target = os.path.samefile(value['Target'], expected_binary)
                    same_hash = hashlib.sha256(pathlib.Path(value['Target']).read_bytes()).digest() == hashlib.sha256(expected_binary.read_bytes()).digest()
                except OSError:
                    same_target = same_hash = False
                if not same_target or not same_hash or value['Description'] != 'Findrail managed shortcut':
                    raise AssertionError(f"shortcut target/provenance mismatch (target_exists={pathlib.Path(value['Target']).exists()}, expected_exists={expected_binary.exists()}, target_tail={pathlib.Path(value['Target']).parts[-4:]!r}, expected_tail={expected_binary.parts[-4:]!r})")
            inspect_expected = lambda tree: inspect(tree)
        else:
            raise RuntimeError(f'unsupported native OS: {os_name}')

        binary = first / ('findrail-desktop.exe' if os.name == 'nt' else ('Findrail.app/Contents/MacOS/Findrail' if os_name == 'Darwin' else 'findrail-desktop'))
        records = []
        def save_report(status='running'):
            report = {'os': os_name, 'architecture': platform.machine(), 'candidate_sha': args.candidate_sha,
                      'candidate_version': info['version'], 'archive_sha256': hashlib.sha256(archive.read_bytes()).hexdigest(),
                      'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'scope': 'disposable owned destination; synthetic documents/data only',
                      'status': status, 'cases': records}
            CaseRecorder(report, args.report).save()
            return report
        def case(case_id, callback):
            start = time.monotonic()
            current = {'id': case_id, 'status': 'running'}
            records.append(current)
            save_report()
            try:
                callback()
            except BaseException:
                current.update(status='failed', duration_ms=round((time.monotonic()-start)*1000))
                save_report('failed')
                raise
            current.update(status='passed', duration_ms=round((time.monotonic()-start)*1000))
            save_report()
        data = root / 'existing data'
        originals = root / 'original documents'; originals.mkdir(); (originals / 'note.md').write_text('synthetic original')
        cli = first / ('findrail.exe' if os.name == 'nt' else 'findrail')
        save_report()
        try:
            command([str(cli), 'index', '--data-dir', str(data), str(originals)], timeout=30)
        except BaseException:
            save_report('failed')
            raise
        (data / 'synthetic collections-notes-report').write_text('synthetic preserved data')
        before_data, before_originals = directory_fingerprint(data), directory_fingerprint(originals)
        case('install', lambda: (run(first, script_name), inspect_expected(first)))
        case('repeat-install', lambda: (run(first, script_name), inspect_expected(first)))
        case('update-new-extraction', lambda: (run(second, script_name), inspect_expected(second)))
        def uninstall():
            run(second, uninstall_name)
            if entry.exists() or entry.is_symlink():
                raise AssertionError('uninstall left installed resource')
        case('uninstall', uninstall)
        case('repeat-uninstall', uninstall)
        def preserve_data():
            if directory_fingerprint(data) != before_data or directory_fingerprint(originals) != before_originals:
                raise AssertionError('stopped data directory or original bytes changed')
        case('data-and-originals-preserved', preserve_data)
        def reopen_existing_data():
            run(second, script_name); inspect_expected(second)
            listing = command([str(cli), 'sources', '--data-dir', str(data), '--json'], timeout=30)
            if len(json.loads(listing.stdout)['sources']) != 1:
                raise AssertionError('existing data directory did not reopen after reinstall')
        case('reinstall-existing-data-reopen', reopen_existing_data)
        run(second, uninstall_name)
        def reject_collision():
            if os_name == 'Darwin':
                entry.mkdir(); sentinel = entry / 'foreign.txt'; sentinel.write_text('leave me')
            else:
                entry.write_text('foreign resource'); sentinel = entry
            if os_name == 'Linux':
                result = command(['sh', str(second / resource / script_name), '--destination', str(destination)], ok=False, timeout=45)
            elif os_name == 'Darwin':
                result = command(['sh', str(second / resource / script_name), '--destination', str(destination)], ok=False, timeout=45)
            else:
                result = command([ps, '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', str(second / resource / script_name), '-Binary', str(second / 'findrail-desktop.exe'), '-DestinationDirectory', str(destination)], ok=False, timeout=45)
            if result.returncode == 0: raise AssertionError('installer accepted a foreign resource')
            expected = 'leave me' if os_name == 'Darwin' else 'foreign resource'
            if sentinel.read_text() != expected: raise AssertionError('foreign resource was changed')
            uninstall_result = command(['sh', str(second / resource / uninstall_name), '--destination', str(destination)] if os_name != 'Windows' else [ps, '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', str(second / resource / uninstall_name), str(destination)], ok=False, timeout=45)
            if uninstall_result.returncode == 0 or sentinel.read_text() != expected:
                raise AssertionError('uninstaller did not preserve foreign resource')
            if os_name == 'Darwin': shutil.rmtree(entry)
            else: entry.unlink()
        case('foreign-collision-and-uninstall-refused', reject_collision)
        report = save_report('passed')
        encoded = json.dumps(report, sort_keys=True, indent=2) + '\n'
        print(encoded, end='')


if __name__ == '__main__':
    def interrupted(signum, frame):
        raise KeyboardInterrupt('installer verification interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    main()
