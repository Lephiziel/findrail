#!/usr/bin/env python3
"""Build deterministic Findrail installation archives."""
import argparse
import hashlib
import json
import os
import pathlib
import re
import shutil
import stat
import subprocess
import tarfile
import tempfile
import time
import zipfile

TARGETS = ['linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64', 'windows-amd64']
ROOT = pathlib.Path(__file__).resolve().parents[1]
GO_LICENSE_FALLBACK = '''Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
'''


def run(command, **kwargs):
    return subprocess.check_output(command, text=True, encoding='utf-8', **kwargs).strip()


def safe_basename(name):
    return bool(name) and pathlib.PurePosixPath(name).name == name and name not in ('.', '..') and not any(c in name for c in '\\/:\n\r')


def verify_manifest(directory):
    directory = pathlib.Path(directory)
    manifest = directory / 'SHA256SUMS.txt'
    seen = set()
    entries = []
    for line in manifest.read_text(encoding='utf-8').splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([^\s]+)', line)
        if not match or not safe_basename(match[2]) or match[2] == 'SHA256SUMS.txt' or match[2] in seen:
            raise ValueError('invalid checksum manifest entry')
        seen.add(match[2])
        path = directory / match[2]
        if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != match[1]:
            raise ValueError('missing or mismatched package: ' + match[2])
        entries.append(match[2])
    if not entries or entries != sorted(entries):
        raise ValueError('empty or unsorted checksum manifest')
    return entries


def extract_verified(archive, destination):
    """Extract only regular files belonging to one top-level package folder."""
    archive, destination = pathlib.Path(archive), pathlib.Path(destination)
    if destination.exists():
        raise ValueError('extraction destination must be fresh')
    destination.mkdir(parents=True)
    root = None
    seen = set()
    if archive.name.endswith('.zip'):
        with zipfile.ZipFile(archive) as zf:
            for item in zf.infolist():
                p = pathlib.PurePosixPath(item.filename)
                mode = item.external_attr >> 16
                if p.is_absolute() or '..' in p.parts or not p.parts or '\\' in item.filename or ':' in item.filename or item.filename in seen:
                    raise ValueError('unsafe archive member')
                seen.add(item.filename)
                if mode and (stat.S_ISLNK(mode) or (item.is_dir() and not stat.S_ISDIR(mode)) or
                             (not item.is_dir() and not stat.S_ISREG(mode))):
                    raise ValueError('unsafe archive member type')
                if root is None: root = p.parts[0]
                if p.parts[0] != root:
                    raise ValueError('unexpected archive member type or root')
            if not root: raise ValueError('empty archive')
            zf.extractall(destination)
    else:
        with tarfile.open(archive, 'r:gz') as tf:
            for item in tf.getmembers():
                p = pathlib.PurePosixPath(item.name)
                if p.is_absolute() or '..' in p.parts or not p.parts or '\\' in item.name or ':' in item.name or item.name in seen or not (item.isfile() or item.isdir()):
                    raise ValueError('unsafe archive member')
                seen.add(item.name)
                if root is None: root = p.parts[0]
                if p.parts[0] != root: raise ValueError('unexpected archive root')
            if not root: raise ValueError('empty archive')
            tf.extractall(destination, filter='data')
    return destination / root


def verify_archive(archive, target, version):
    archive = pathlib.Path(archive)
    stem = f'findrail_{version}_{target}'
    expected_suffix = '.zip' if target.startswith('windows-') else '.tar.gz'
    if archive.name != stem + expected_suffix:
        raise ValueError('archive name does not match expected target/version')
    with tempfile.TemporaryDirectory(prefix='findrail-verify-') as temp:
        root = extract_verified(archive, pathlib.Path(temp) / 'extract')
        if root.name != stem:
            raise ValueError('unexpected package root')
        meta = json.loads((root / 'BUILD-INFO.json').read_text(encoding='utf-8'))
        revision = meta.get('revision', '')
        if meta.get('version') != version or meta.get('target') != target or not (revision == 'unknown' or re.fullmatch(r'[0-9a-f]{40}', revision)):
            raise ValueError('candidate metadata mismatch')
        binary = root / ('findrail.exe' if target.startswith('windows-') else 'findrail')
        if not binary.is_file(): raise ValueError('package executable missing')
        if not target.startswith('windows-') and not binary.stat().st_mode & 0o111:
            raise ValueError('Unix package executable is not executable')
        required = {'INSTALL.txt', 'LICENSE', 'NOTICE', 'BUILD-INFO.json', binary.name, 'licenses/DEPENDENCIES.txt', 'licenses/Go-LICENSE.txt'}
        members = {p.relative_to(root).as_posix() for p in root.rglob('*') if p.is_file()}
        if not required.issubset(members):
            raise ValueError('package is missing required installation or license files')
        if any(name not in required and not (name.startswith('licenses/') and '/' not in name[len('licenses/'):]
                                             and name.endswith('.txt')) for name in members):
            raise ValueError('package contains an unexpected member')
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', default=(ROOT / 'build/package/VERSION').read_text().strip())
    parser.add_argument('--go', default='go')
    parser.add_argument('--target', choices=TARGETS, action='append')
    parser.add_argument('--output', type=pathlib.Path, default=ROOT / 'dist')
    parser.add_argument('--source-date-epoch', type=int)
    parser.add_argument('--verify-only', action='store_true')
    parser.add_argument('--require-clean', action='store_true', help='require known clean Git provenance for publication builds')
    args = parser.parse_args()
    output = args.output.resolve()
    if args.verify_only:
        try:
            print('\n'.join(verify_manifest(output)))
        except (OSError, ValueError) as error:
            parser.error('package verification failed: ' + str(error))
        return
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?', args.version):
        parser.error('Version must be a semantic version without a leading v')
    targets = args.target or TARGETS
    if len(set(targets)) != len(targets): parser.error('duplicate --target is not allowed')
    go = shutil.which(args.go)
    if not go: parser.error('Go executable not found')
    try:
        revision = run(['git', 'rev-parse', 'HEAD'], cwd=ROOT)
        dirty = bool(run(['git', 'status', '--porcelain'], cwd=ROOT))
        commit_epoch = int(run(['git', 'show', '-s', '--format=%ct', 'HEAD'], cwd=ROOT))
    except (subprocess.CalledProcessError, OSError):
        revision, dirty, commit_epoch = 'unknown', True, 0
    if args.require_clean and (dirty or not re.fullmatch(r'[0-9a-f]{40}', revision)):
        parser.error('publication packages require a known source revision and clean checkout')
    epoch = args.source_date_epoch if args.source_date_epoch is not None else commit_epoch
    if epoch < 315532800: parser.error('SOURCE_DATE_EPOCH must be >= 1980-01-01 (ZIP timestamp limit)')
    output.parent.mkdir(parents=True, exist_ok=True)
    output.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.findrail-package-', dir=output.parent) as temp:
        staging = pathlib.Path(temp)
        common = staging / 'common'; common.mkdir()
        for name in ('LICENSE', 'NOTICE'):
            shutil.copyfile(ROOT / name, common / name)
        shutil.copyfile(ROOT / 'build/package/INSTALL.txt', common / 'INSTALL.txt')
        licenses = common / 'licenses'; licenses.mkdir()
        goroot = pathlib.Path(run([go, 'env', 'GOROOT']))
        go_license = goroot / 'LICENSE'
        if go_license.is_file():
            shutil.copyfile(go_license, licenses / 'Go-LICENSE.txt')
        else:
            # Some distro-packaged Go toolchains omit GOROOT/LICENSE. Preserve
            # the Go distribution's BSD notice instead of silently dropping it.
            (licenses / 'Go-LICENSE.txt').write_text(GO_LICENSE_FALLBACK, encoding='utf-8', newline='\n')
        subprocess.run([go, 'mod', 'download', 'all'], cwd=ROOT, check=True)
        mods = run([go, 'list', '-m', '-json', 'all'], cwd=ROOT)
        decoder = json.JSONDecoder(); dependencies = []
        while mods.strip():
            item, end = decoder.raw_decode(mods.lstrip()); mods = mods.lstrip()[end:]
            if item.get('Main'): continue
            folder = pathlib.Path(item['Dir'])
            sources = sorted(p for p in folder.iterdir() if p.is_file() and p.name.upper().startswith(('LICENSE', 'COPYING')))
            if not sources: raise RuntimeError('Missing license notice for ' + item['Path'])
            dependencies.append(item['Path'] + ' ' + item['Version'])
            prefix = re.sub(r'[^A-Za-z0-9.-]', '_', item['Path'])
            for source in sources: shutil.copyfile(source, licenses / (prefix + '-' + source.name + '.txt'))
        (licenses / 'DEPENDENCIES.txt').write_text('\n'.join(sorted(dependencies)) + '\n', encoding='utf-8', newline='\n')
        artifacts = []
        for target in targets:
            os_name, arch = target.split('-'); stem = f'findrail_{args.version}_{target}'
            folder = staging / stem; shutil.copytree(common, folder)
            meta = {'version': args.version, 'revision': revision, 'dirty': dirty, 'target': target,
                    'go_version': run([go, 'version']), 'source_date_epoch': epoch}
            (folder / 'BUILD-INFO.json').write_text(json.dumps(meta, sort_keys=True, indent=2) + '\n', encoding='utf-8', newline='\n')
            binary = folder / ('findrail.exe' if os_name == 'windows' else 'findrail')
            env = {**os.environ, 'GOOS': os_name, 'GOARCH': arch, 'CGO_ENABLED': '0'}
            subprocess.run([go, 'build', '-trimpath', '-ldflags', f'-s -w -buildid= -X main.version={args.version}', '-o', str(binary), './cmd/findrail'], cwd=ROOT, env=env, check=True)
            if os_name != 'windows': binary.chmod(0o755)
            archive = staging / (stem + ('.zip' if os_name == 'windows' else '.tar.gz'))
            if os_name == 'windows':
                dt = time.gmtime(epoch)[:6]; dt = (dt[0], dt[1], dt[2], dt[3], dt[4], dt[5] // 2 * 2)
                with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
                    for file in sorted(folder.rglob('*')):
                        if file.is_file():
                            info = zipfile.ZipInfo(file.relative_to(staging).as_posix(), dt); info.external_attr = (0o100755 if file == binary else 0o100644) << 16
                            zf.writestr(info, file.read_bytes())
            else:
                raw = archive.open('wb')
                import gzip
                with raw, gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=epoch, compresslevel=9) as gz:
                    with tarfile.open(fileobj=gz, mode='w|') as tf:
                        for file in sorted(p for p in folder.rglob('*') if p.is_file()):
                            info = tf.gettarinfo(str(file), arcname=file.relative_to(staging).as_posix())
                            info.uid = info.gid = 0; info.uname = info.gname = ''; info.mtime = epoch
                            info.mode = 0o755 if file == binary else 0o644
                            with file.open('rb') as stream: tf.addfile(info, stream)
            verify_archive(archive, target, args.version)
            artifacts.append((archive, archive.name))
        # Publish only explicit outputs; refuse to overwrite successful packages.
        names = [src.name for src, _ in artifacts]
        if len(set(names)) != len(names): raise RuntimeError('duplicate archive output')
        if any((output / name).exists() for name in names + ['SHA256SUMS.txt']): raise RuntimeError('output already contains candidate artifacts')
        moved = []
        try:
            for src, _ in artifacts:
                name = src.name
                dest = output / name; os.replace(src, dest); moved.append(dest)
            manifest_tmp = output / '.SHA256SUMS.txt.tmp'
            manifest_tmp.write_text(''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in sorted(moved, key=lambda p: p.name)), encoding='utf-8', newline='\n')
            os.replace(manifest_tmp, output / 'SHA256SUMS.txt')
        except Exception:
            for path in moved: path.unlink(missing_ok=True)
            (output / '.SHA256SUMS.txt.tmp').unlink(missing_ok=True)
            raise
    print('\n'.join(names + ['SHA256SUMS.txt']), flush=True)


if __name__ == '__main__':
    main()
