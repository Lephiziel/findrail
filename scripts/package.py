#!/usr/bin/env python3
"""Build installation archives without requiring Go on the user's computer."""
import argparse
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

TARGETS = ['linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64', 'windows-amd64']
ROOT = pathlib.Path(__file__).resolve().parents[1]


def modules(go):
    # Module metadata may exist without source archives on a clean CI runner.
    # Populate the complete graph before collecting dependency license notices.
    subprocess.run([go, 'mod', 'download', 'all'], cwd=ROOT, check=True)
    raw = subprocess.check_output([go, 'list', '-m', '-json', 'all'], cwd=ROOT, text=True, encoding="utf-8")
    decoder = json.JSONDecoder()
    while raw.strip():
        item, end = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[end:]
        yield item


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', default=(ROOT / 'build/package/VERSION').read_text().strip())
    parser.add_argument('--go', default='go')
    parser.add_argument('--target', choices=TARGETS, action='append')
    parser.add_argument('--output', type=pathlib.Path, default=ROOT / 'dist')
    args = parser.parse_args()
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?', args.version):
        parser.error('Version must be a semantic version without a leading v')
    go = shutil.which(args.go)
    if not go:
        parser.error('Go executable not found')
    args.output.mkdir(parents=True, exist_ok=True)
    archives = []
    with tempfile.TemporaryDirectory(prefix='findrail-package-') as temporary:
        staging = pathlib.Path(temporary)
        common = staging / 'common'
        common.mkdir()
        for name in ('LICENSE', 'NOTICE'):
            shutil.copy2(ROOT / name, common / name)
        shutil.copy2(ROOT / 'build/package/INSTALL.txt', common / 'INSTALL.txt')
        licenses = common / 'licenses'
        licenses.mkdir()
        goroot = pathlib.Path(subprocess.check_output([go, 'env', 'GOROOT'], text=True, encoding="utf-8").strip())
        shutil.copy2(goroot / 'LICENSE', licenses / 'Go-LICENSE.txt')
        dependencies = []
        for item in modules(go):
            if item.get('Main'):
                continue
            directory = pathlib.Path(item['Dir'])
            candidates = sorted(p for p in directory.iterdir() if p.is_file()
                                and p.name.upper().startswith(('LICENSE', 'COPYING')))
            if not candidates:
                raise RuntimeError('Missing license notice for ' + item['Path'])
            dependencies.append(item['Path'] + ' ' + item['Version'])
            prefix = re.sub(r'[^A-Za-z0-9.-]', '_', item['Path'])
            for source in candidates:
                shutil.copy2(source, licenses / (prefix + '-' + source.name + '.txt'))
        (licenses / 'DEPENDENCIES.txt').write_text('\n'.join(dependencies) + '\n', encoding='utf-8')
        for target in args.target or TARGETS:
            os_name, arch = target.split('-')
            name = f'findrail_{args.version}_{target}'
            folder = staging / name
            shutil.copytree(common, folder)
            binary = folder / ('findrail.exe' if os_name == 'windows' else 'findrail')
            env = {**os.environ, 'GOOS': os_name, 'GOARCH': arch, 'CGO_ENABLED': '0'}
            subprocess.run([go, 'build', '-trimpath', '-ldflags', f'-s -w -X main.version={args.version}',
                            '-o', str(binary), './cmd/findrail'], cwd=ROOT, env=env, check=True)
            if os_name != 'windows':
                binary.chmod(0o755)
            suffix = '.zip' if os_name == 'windows' else '.tar.gz'
            archive = args.output / (name + suffix)
            if os_name == 'windows':
                with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as output:
                    for file in sorted(folder.rglob('*')):
                        if file.is_file():
                            output.write(file, file.relative_to(staging))
            else:
                with tarfile.open(archive, 'w:gz') as output:
                    output.add(folder, arcname=name)
            archives.append(archive)
            print(archive.name, flush=True)
    (args.output / 'SHA256SUMS.txt').write_text(''.join(
        hashlib.sha256(path.read_bytes()).hexdigest() + '  ' + path.name + '\n' for path in archives), encoding='utf-8')


if __name__ == '__main__':
    main()
