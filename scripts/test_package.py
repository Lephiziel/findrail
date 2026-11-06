import ctypes
import ctypes.util
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
import zipfile
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import package


class PackageSafetyTests(unittest.TestCase):
    def test_beta_freeze_version_matches_candidate_without_schema_change(self):
        root = pathlib.Path(package.__file__).resolve().parents[1]
        version = (root / 'build/package/VERSION').read_text().strip()
        candidate_version = (root / 'build/package/CANDIDATE_VERSION').read_text().strip()
        self.assertEqual(version, '0.1.0-beta.1')
        self.assertEqual(candidate_version, version)
        self.assertEqual(package.schema_version(), 8)

    def test_platform_launcher_allowlists_are_explicit_and_present(self):
        self.assertEqual(set(package.RESOURCE_FILES), {'linux', 'darwin', 'windows'})
        for platform, members in package.RESOURCE_FILES.items():
            with self.subTest(platform=platform):
                self.assertTrue(members)
                for source, destination in members.items():
                    self.assertTrue((package.ROOT / source).is_file(), source)
                    self.assertNotIn('..', pathlib.PurePosixPath(destination).parts)
        self.assertIn('Findrail.app/Contents/Info.plist', package.RESOURCE_FILES['darwin'].values())
        self.assertIn('launcher/linux/findrail.desktop', package.RESOURCE_FILES['linux'].values())

    @unittest.skipIf(os.name == 'nt', 'POSIX desktop installer')
    def test_linux_user_installer_escapes_package_paths_and_keeps_data_separate(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = pathlib.Path(temporary)
            package_root = base / 'Findrail space & 100% "quoted" \\ back $dollar `tick`' / 'candidate'
            resources = package_root / 'launcher/linux'
            resources.mkdir(parents=True)
            for name in ('findrail.desktop', 'install-user.sh', 'uninstall-user.sh'):
                shutil.copyfile(package.ROOT / 'packaging/desktop/linux' / name, resources / name)
            launcher = package_root / 'findrail-desktop'
            launcher.write_text(f'#!{sys.executable}\nimport json,os,sys\n'
                               'path=os.environ["FINDRAIL_TEST_LAUNCH_LOG"]\n'
                               'with open(path+".tmp","w") as f:json.dump(sys.argv,f)\n'
                               'os.replace(path+".tmp",path)\n', encoding='utf-8')
            launcher.chmod(0o755)
            home, xdg = base / 'home', base / 'xdg'
            home.mkdir()
            env = {**os.environ, 'HOME': str(home), 'XDG_DATA_HOME': str(xdg)}
            result = subprocess.run(['sh', str(resources / 'install-user.sh')], env=env,
                                    capture_output=True, text=True, check=True)
            installed = (xdg / 'applications/findrail.desktop').read_text(encoding='utf-8')
            self.assertNotIn('@INSTALL_DIR@', installed)
            self.assertIn('Installed user menu entry', result.stdout)
            self.assertFalse((home / '.local/share/findrail').exists())
            # GLib is the real desktop-entry parser on Linux. Verify the
            # installed command, rather than mirroring its escaping algorithm.
            library = ctypes.util.find_library('gio-2.0') if sys.platform == 'linux' else None
            if library:
                gio = ctypes.CDLL(library)
                gio.g_desktop_app_info_new_from_filename.argtypes = [ctypes.c_char_p]
                gio.g_desktop_app_info_new_from_filename.restype = ctypes.c_void_p
                gio.g_app_info_launch.argtypes = [ctypes.c_void_p] * 4
                gio.g_app_info_launch.restype = ctypes.c_int
                gio.g_object_unref.argtypes = [ctypes.c_void_p]
                app = gio.g_desktop_app_info_new_from_filename(os.fsencode(xdg / 'applications/findrail.desktop'))
                self.assertTrue(app, 'installed desktop entry must be accepted by GLib')
                log = base / 'launched.json'
                try:
                    with patch.dict(os.environ, {'FINDRAIL_TEST_LAUNCH_LOG': str(log)}):
                        self.assertEqual(gio.g_app_info_launch(app, None, None, None), 1)
                        deadline = time.monotonic() + 3
                        while not log.exists() and time.monotonic() < deadline:
                            time.sleep(.02)
                        self.assertTrue(log.exists(), 'desktop entry did not execute the intended launcher')
                    self.assertEqual(json.loads(log.read_text()), [str(launcher)])
                finally:
                    gio.g_object_unref(app)

    @unittest.skipIf(os.name == 'nt', 'POSIX desktop installer')
    def test_linux_install_update_uninstall_owns_only_its_resource(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            destination = root / 'owned menu'; destination.mkdir()
            home = root / 'unmodified-home'; home.mkdir()
            def archive_tree(label):
                package_root = root / label; resources = package_root / 'launcher/linux'; resources.mkdir(parents=True)
                for name in ('findrail.desktop', 'install-user.sh', 'uninstall-user.sh'):
                    shutil.copyfile(package.ROOT / 'packaging/desktop/linux' / name, resources / name)
                launcher = package_root / 'findrail-desktop'; launcher.write_text('#!/bin/sh\n'); launcher.chmod(0o755)
                return resources
            first, second = archive_tree('первый 📦'), archive_tree('second $ % " \\')
            install = lambda scripts: subprocess.run(['sh', str(scripts / 'install-user.sh'), '--destination', str(destination)], capture_output=True, text=True)
            uninstall = lambda scripts: subprocess.run(['sh', str(scripts / 'uninstall-user.sh'), '--destination', str(destination)], capture_output=True, text=True)
            resource = destination / 'findrail.desktop'
            self.assertEqual(install(first).returncode, 0)
            self.assertEqual(install(second).returncode, 0)
            expected_tryexec = str(second.parent.parent / 'findrail-desktop').replace('\\', '\\\\')
            self.assertIn('TryExec=' + expected_tryexec, resource.read_text())
            data = root / 'data'; data.mkdir(); (data / 'collections-notes-report').write_text('preserve')
            original = root / 'original.md'; original.write_text('preserve')
            self.assertEqual(uninstall(second).returncode, 0)
            self.assertEqual(uninstall(second).returncode, 0)
            self.assertEqual((data / 'collections-notes-report').read_text(), 'preserve')
            self.assertEqual(original.read_text(), 'preserve')
            foreign = destination / 'findrail.desktop'; foreign.write_text('not Findrail')
            self.assertNotEqual(install(first).returncode, 0)
            self.assertEqual(foreign.read_text(), 'not Findrail')
            foreign.unlink(); foreign.symlink_to(original)
            self.assertNotEqual(uninstall(first).returncode, 0)
            self.assertTrue(foreign.is_symlink())

    @unittest.skipIf(os.name == 'nt', 'POSIX app installer')
    def test_macos_reinstall_updates_one_app_and_rejects_symlink_destination(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            package_root = root / 'package'
            resources = package_root / 'launcher/macos'
            resources.mkdir(parents=True)
            installer = resources / 'install-user.sh'
            shutil.copyfile(package.ROOT / 'packaging/desktop/macos/install-user.sh', installer)
            source = package_root / 'Findrail.app/Contents/MacOS'
            source.mkdir(parents=True)
            executable = source / 'Findrail'
            executable.write_text('first')
            home = root / 'user'
            env = {**os.environ, 'HOME': str(home)}
            subprocess.run(['sh', str(installer)], env=env, check=True, capture_output=True)
            executable.write_text('second')
            subprocess.run(['sh', str(installer)], env=env, check=True, capture_output=True)
            installed = home / 'Applications/Findrail.app'
            self.assertEqual((installed / 'Contents/MacOS/Findrail').read_text(), 'second')
            self.assertFalse((installed / 'Findrail.app').exists())
            shutil.rmtree(installed)
            other = root / 'unrelated'; other.mkdir()
            installed.symlink_to(other, target_is_directory=True)
            result = subprocess.run(['sh', str(installer)], env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(list(other.iterdir()), [])

    def test_go_license_fallback_retains_all_three_conditions(self):
        self.assertIn('Neither the name of Google LLC', package.GO_LICENSE_FALLBACK)
        self.assertIn('specific prior written permission', package.GO_LICENSE_FALLBACK)

    @unittest.skipIf(os.name == 'nt', 'POSIX app installer')
    def test_macos_update_preserves_foreign_predictable_backup_directory(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            resources = root/'package/launcher/macos'
            resources.mkdir(parents=True)
            installer = resources/'install-user.sh'
            shutil.copyfile(package.ROOT/'packaging/desktop/macos/install-user.sh', installer)
            new = root/'package/Findrail.app/Contents/MacOS'
            new.mkdir(parents=True)
            (new/'Findrail').write_text('new executable')
            destination = root/'owned applications'
            old = destination/'Findrail.app/Contents'
            old.mkdir(parents=True)
            (old/'.findrail-managed').write_text('org.findrail.desktop')
            wrapper = ('foreign="$1/.Findrail.app.previous.$$"; mkdir "$foreign"; '
                       'printf "foreign data" > "$foreign/keep"; '
                       'exec sh "$2" --destination "$1"')
            subprocess.run(['sh','-c',wrapper,'sh',str(destination),str(installer)],
                           check=True, capture_output=True, timeout=5)
            kept = list(destination.glob('.Findrail.app.previous.*/keep'))
            self.assertEqual(len(kept), 1)
            self.assertEqual(kept[0].read_text(), 'foreign data')
            self.assertEqual((destination/'Findrail.app/Contents/MacOS/Findrail').read_text(), 'new executable')

    @unittest.skipIf(os.name == 'nt', 'POSIX app installer')
    def test_macos_failed_publication_restores_previous_app(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            resources = root/'package/launcher/macos'
            resources.mkdir(parents=True)
            installer = resources/'install-user.sh'
            shutil.copyfile(package.ROOT/'packaging/desktop/macos/install-user.sh', installer)
            new = root/'package/Findrail.app/Contents/MacOS'
            new.mkdir(parents=True)
            (new/'Findrail').write_text('new executable')
            destination = root/'owned applications'
            old = destination/'Findrail.app/Contents'
            (old/'MacOS').mkdir(parents=True)
            (old/'MacOS/Findrail').write_text('previous executable')
            (old/'.findrail-managed').write_text('org.findrail.desktop')
            adapters = root/'adapters'; adapters.mkdir()
            real_mv = shutil.which('mv')
            (adapters/'mv').write_text('#!/bin/sh\ncase "$1" in */new-app) exit 1;; esac\n'
                                      'exec "$FINDRAIL_REAL_MV" "$@"\n')
            (adapters/'mv').chmod(0o755)
            env = {**os.environ, 'PATH':str(adapters)+os.pathsep+os.environ['PATH'],
                   'FINDRAIL_REAL_MV':real_mv}
            result = subprocess.run(['sh',str(installer),'--destination',str(destination)],
                                    env=env, capture_output=True, timeout=5)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((old/'MacOS/Findrail').read_text(), 'previous executable')
            self.assertEqual(list(destination.glob('.Findrail.app.stage.*')), [])

    def test_manifest_rejects_paths_duplicates_and_mismatch(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            (root / 'candidate.tar.gz').write_bytes(b'archive')
            digest = package.hashlib.sha256(b'archive').hexdigest()
            for text in (f'{digest}  ../escape.tar.gz\n', f'{digest}  candidate.tar.gz\n{digest}  candidate.tar.gz\n', '0' * 64 + '  candidate.tar.gz\n'):
                (root / 'SHA256SUMS.txt').write_text(text)
                with self.assertRaises(ValueError): package.verify_manifest(root)

    def test_extract_rejects_traversal_and_symlink(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp); archive = root / 'bad.tar.gz'
            with tarfile.open(archive, 'w:gz') as tf:
                info = tarfile.TarInfo('pkg/../../escape'); info.size = 1
                import io
                tf.addfile(info, io.BytesIO(b'x'))
            with self.assertRaises(ValueError): package.extract_verified(archive, root / 'out')
            with tarfile.open(archive, 'w:gz') as tf:
                info = tarfile.TarInfo('pkg/link'); info.type = tarfile.SYMTYPE; info.linkname = '../../escape'
                tf.addfile(info)
            with self.assertRaises(ValueError): package.extract_verified(archive, root / 'out')

    def test_zip_rejects_windows_separator_traversal(self):
        with tempfile.TemporaryDirectory() as temp:
            archive = pathlib.Path(temp) / 'bad.zip'
            with zipfile.ZipFile(archive, 'w') as zf:
                zf.writestr('pkg/..\\..\\escape', b'bad')
            with self.assertRaises(ValueError): package.extract_verified(archive, pathlib.Path(temp) / 'out')

    def test_extract_rejects_duplicate_and_file_directory_conflicts(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            for members in ((('pkg/a', b'a'), ('pkg/a', b'b')),
                            (('pkg/A', b'a'), ('pkg/a', b'b')),
                            (('pkg/a', b'a'), ('pkg/a/b', b'b'))):
                archive = root / 'bad.tar.gz'
                with tarfile.open(archive, 'w:gz') as tf:
                    for name, data in members:
                        info = tarfile.TarInfo(name); info.size = len(data)
                        import io
                        tf.addfile(info, io.BytesIO(data))
                with self.subTest(members=members), self.assertRaises(ValueError):
                    package.extract_verified(archive, root / ('out-' + str(len(list(root.iterdir())))))

    def test_extract_rejects_file_over_an_explicit_child_directory(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp); archive = root / 'bad.tar.gz'
            with tarfile.open(archive, 'w:gz') as tf:
                info = tarfile.TarInfo('pkg/a/b'); info.type = tarfile.DIRTYPE
                tf.addfile(info)
                info = tarfile.TarInfo('pkg/a'); info.size = 1
                import io
                tf.addfile(info, io.BytesIO(b'x'))
            with self.assertRaises(ValueError):
                package.extract_verified(archive, root / 'out')

    def test_safe_basename(self):
        for name in ('../a', 'a/b', 'C:\\a', ''):
            self.assertFalse(package.safe_basename(name))
        self.assertTrue(package.safe_basename('findrail_0.1.0-alpha.3_linux-amd64.tar.gz'))


if __name__ == '__main__':
    unittest.main()
