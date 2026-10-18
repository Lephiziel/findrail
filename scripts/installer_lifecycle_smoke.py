#!/usr/bin/env python3
"""Exercise packaged per-user installer resources in a disposable native destination."""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import shutil
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import package


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('archive', type=pathlib.Path)
    parser.add_argument('--candidate-sha', required=True)
    parser.add_argument('--report', type=pathlib.Path)
    args = parser.parse_args()
    archive = args.archive.resolve()
    with tempfile.TemporaryDirectory(prefix='findrail install 📦 ') as temp:
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
        first_name = 'первый путь % $' if os.name == 'nt' else 'первый путь % "$ \\'
        first = package.extract_verified(archive, root / first_name)
        second = package.extract_verified(archive, root / 'updated extraction 📦')
        info = json.loads((first / 'BUILD-INFO.json').read_text(encoding='utf-8'))
        if info.get('revision') != args.candidate_sha or info.get('dirty') is not False:
            raise ValueError('verified archive provenance does not match candidate SHA / clean source')
        os_name = platform.system()
        if os_name == 'Linux':
            resource = pathlib.Path('launcher/linux')
            destination = root / 'owned applications'
            destination.mkdir()
            script_name, uninstall_name = 'install-user.sh', 'uninstall-user.sh'
            run = lambda tree, name: subprocess.run(['sh', str(tree / resource / name), '--destination', str(destination)], check=True, capture_output=True, text=True)
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
            run = lambda tree, name: subprocess.run(['sh', str(tree / resource / name), '--destination', str(destination)], check=True, capture_output=True, text=True)
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
            def run(tree, name):
                script = tree / resource / name
                command = [ps, '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', str(script)]
                if name == script_name:
                    command += ['-Binary', str(tree / 'findrail-desktop.exe'), '-DestinationDirectory', str(destination)]
                else:
                    command += [str(destination)]
                result = subprocess.run(command, capture_output=True, text=True)
                if result.returncode:
                    detail = (result.stderr or result.stdout or 'no PowerShell diagnostic').strip()
                    for private_path in (str(root), os.environ.get('USERPROFILE', ''), os.environ.get('RUNNER_TEMP', '')):
                        if private_path:
                            detail = detail.replace(private_path, '<runner-path>')
                    raise RuntimeError(f'PowerShell installer failed ({result.returncode}): {detail}')
                return result
            entry = destination / 'Findrail.lnk'
            def inspect(tree):
                script = "$s=(New-Object -ComObject WScript.Shell).CreateShortcut($args[0]); [pscustomobject]@{Target=$s.TargetPath;Description=$s.Description}|ConvertTo-Json -Compress"
                value = json.loads(subprocess.check_output([ps, '-NoProfile', '-Command', script, str(entry)], text=True))
                if pathlib.Path(value['Target']) != tree / 'findrail-desktop.exe' or value['Description'] != 'Findrail managed shortcut':
                    raise AssertionError('shortcut target/provenance mismatch')
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
            if args.report:
                args.report.parent.mkdir(parents=True, exist_ok=True)
                args.report.write_text(json.dumps(report, sort_keys=True, indent=2) + '\n', encoding='utf-8')
        def case(case_id, callback):
            start = time.monotonic()
            try:
                callback()
            except BaseException:
                records.append({'id': case_id, 'status': 'failed', 'duration_ms': round((time.monotonic()-start)*1000)})
                save_report('failed')
                raise
            records.append({'id': case_id, 'status': 'passed', 'duration_ms': round((time.monotonic()-start)*1000)})
            save_report()
        data = root / 'existing data'
        originals = root / 'original documents'; originals.mkdir(); (originals / 'note.md').write_text('synthetic original')
        cli = first / ('findrail.exe' if os.name == 'nt' else 'findrail')
        subprocess.run([str(cli), 'index', '--data-dir', str(data), str(originals)], check=True, capture_output=True, text=True, timeout=30)
        (data / 'synthetic collections-notes-report').write_text('synthetic preserved data')
        case('install', lambda: (run(first, script_name), inspect_expected(first)))
        case('repeat-install', lambda: (run(first, script_name), inspect_expected(first)))
        case('update-new-extraction', lambda: (run(second, script_name), inspect_expected(second)))
        case('uninstall', lambda: run(second, uninstall_name))
        case('repeat-uninstall', lambda: run(second, uninstall_name))
        if entry.exists() or entry.is_symlink():
            raise AssertionError('uninstall left installed resource')
        case('data-and-originals-preserved', lambda: (None if (data / 'synthetic collections-notes-report').read_text() == 'synthetic preserved data' and (originals / 'note.md').read_text() == 'synthetic original' else (_ for _ in ()).throw(AssertionError('user data changed'))))
        def reopen_existing_data():
            run(second, script_name); inspect_expected(second)
            listing = subprocess.run([str(cli), 'sources', '--data-dir', str(data), '--json'], check=True, capture_output=True, text=True, timeout=30)
            if len(json.loads(listing.stdout)['sources']) != 1:
                raise AssertionError('existing data directory did not reopen after reinstall')
        case('reinstall-existing-data-reopen', reopen_existing_data)
        run(second, uninstall_name)
        # Run the rejection case without subprocess check=True.
        def reject_collision():
            if os_name == 'Darwin':
                entry.mkdir(); sentinel = entry / 'foreign.txt'; sentinel.write_text('leave me')
            else:
                entry.write_text('foreign resource'); sentinel = entry
            if os_name == 'Linux':
                result = subprocess.run(['sh', str(second / resource / script_name), '--destination', str(destination)], capture_output=True)
            elif os_name == 'Darwin':
                result = subprocess.run(['sh', str(second / resource / script_name), '--destination', str(destination)], capture_output=True)
            else:
                result = subprocess.run([ps, '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', str(second / resource / script_name), '-Binary', str(second / 'findrail-desktop.exe'), '-DestinationDirectory', str(destination)], capture_output=True)
            if result.returncode == 0: raise AssertionError('installer accepted a foreign resource')
            expected = 'leave me' if os_name == 'Darwin' else 'foreign resource'
            if sentinel.read_text() != expected: raise AssertionError('foreign resource was changed')
            uninstall_result = subprocess.run(['sh', str(second / resource / uninstall_name), '--destination', str(destination)] if os_name != 'Windows' else [ps, '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', str(second / resource / uninstall_name), str(destination)], capture_output=True)
            if uninstall_result.returncode == 0 or sentinel.read_text() != expected:
                raise AssertionError('uninstaller did not preserve foreign resource')
            if os_name == 'Darwin': shutil.rmtree(entry)
            else: entry.unlink()
        case('foreign-collision-and-uninstall-refused', reject_collision)
        save_report('passed')
        report = {'os': os_name, 'architecture': platform.machine(), 'candidate_sha': args.candidate_sha,
                  'candidate_version': info['version'], 'archive_sha256': hashlib.sha256(archive.read_bytes()).hexdigest(),
                  'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'scope': 'disposable owned destination; synthetic documents/data only',
                  'status': 'passed', 'cases': records}
        encoded = json.dumps(report, sort_keys=True, indent=2) + '\n'
        if args.report:
            args.report.parent.mkdir(parents=True, exist_ok=True)
            args.report.write_text(encoded, encoding='utf-8')
        print(encoded, end='')


if __name__ == '__main__':
    main()
