import hashlib
import importlib.util
import io
from pathlib import Path
import shutil
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('deploy', Path(__file__).with_name('homeatlas-deploy.py'))
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)


def assets():
    files = {'homeatlas': b'linux executable', 'documents.hap': b'package'}
    files['SHA256SUMS'] = ''.join(
        f'{hashlib.sha256(body).hexdigest()}  {name}\n' for name, body in files.items()).encode()
    return files


def archive(entries):
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode='w:gz') as target:
        for name, body in entries:
            info = tarfile.TarInfo(name)
            if body is None:
                info.type, info.linkname = tarfile.SYMTYPE, '/etc/shadow'
            else:
                info.size = len(body)
            target.addfile(info, None if body is None else io.BytesIO(body))
    stream.seek(0)
    return stream


class ReceiveTests(unittest.TestCase):
    def test_accepts_exact_assets_and_checksums(self):
        with tempfile.TemporaryDirectory() as folder:
            digests = deploy.receive(archive(assets().items()), Path(folder))
            self.assertEqual(digests['documents.hap'], hashlib.sha256(b'package').hexdigest())

    def test_rejects_paths_links_duplicates_missing_and_oversized_assets(self):
        files = list(assets().items())
        cases = [files + [('../escape', b'x')], files + [('/etc/shadow', b'x')],
                 [('homeatlas', None)], files + [files[0]], files[:2],
                 [('SHA256SUMS', b'x' * 4097)]]
        for entries in cases:
            with self.subTest(entries=[name for name, _ in entries]):
                with tempfile.TemporaryDirectory() as folder, self.assertRaises(ValueError):
                    deploy.receive(archive(entries), Path(folder))

    def test_rejects_modified_binary_and_untrusted_checksum_names(self):
        for replacement in (b'0' * 64 + b'  ../../etc/shadow\n', b'',
                            assets()['SHA256SUMS'] * 2):
            files = assets()
            files['SHA256SUMS'] = replacement
            with tempfile.TemporaryDirectory() as folder, self.assertRaises(ValueError):
                deploy.receive(archive(files.items()), Path(folder))
        files = assets()
        files['homeatlas'] = b'tampered'
        with tempfile.TemporaryDirectory() as folder, self.assertRaisesRegex(ValueError, 'Checksum mismatch'):
            deploy.receive(archive(files.items()), Path(folder))


class DeployTests(unittest.TestCase):
    def setUp(self):
        self.folder = tempfile.TemporaryDirectory()
        self.addCleanup(self.folder.cleanup)
        root = Path(self.folder.name).resolve()
        for name, path in {'RELEASES': root / 'releases', 'CURRENT': root / 'current',
                           'DATA': root / 'data', 'STATE': root / 'state'}.items():
            self.enterContext(patch.object(deploy, name, path))
            if name != 'CURRENT':
                path.mkdir()
        self.old = deploy.RELEASES / 'old'
        self.old.mkdir()
        deploy.CURRENT.symlink_to(self.old)
        (deploy.DATA / 'homeatlas.db').write_bytes(b'old schema')
        (deploy.DATA / 'setup-code').write_bytes(b'private setup code')
        self.release = deploy.RELEASES / 'new'
        self.release.mkdir()
        self.digests = {}
        for name, body in assets().items():
            (self.release / name).write_bytes(body)
            self.digests[name] = hashlib.sha256(body).hexdigest()
        self.commands = []
        self.plugin_error = False
        self.snapshot_error = False
        self.enterContext(patch.object(deploy, 'run', side_effect=self.command))
        self.enterContext(patch.object(deploy.subprocess, 'run', return_value=type('Result', (), {'returncode': 0})()))
        self.health = self.enterContext(patch.object(deploy, 'healthy', return_value=True))
        self.enterContext(patch.object(deploy, 'public_host', return_value='labs.example.ts.net:8443'))

    def command(self, *args):
        self.commands.append(args)
        if args[0] == '/bin/cp':
            if self.snapshot_error:
                raise OSError('disk full')
            shutil.copytree(args[2], args[3], symlinks=True)
        elif args[0] == '/usr/sbin/runuser':
            self.assertEqual(args[1:5], ('-u', 'homeatlas', '--', str(self.release / 'homeatlas')))
            if self.plugin_error:
                raise RuntimeError('plugin rejected')
            installed = deploy.DATA / 'plugins/documents'
            installed.mkdir(parents=True)
            shutil.copy2(self.release / 'documents.hap', installed / 'package.zip')
        elif args[1] == 'start' and deploy.CURRENT.resolve() == self.release:
            (deploy.DATA / 'homeatlas.db').write_bytes(b'migrated schema')

    def test_success_retains_stopped_snapshot_and_switches_release(self):
        deploy.deploy(self.release, self.digests)
        self.assertEqual(deploy.CURRENT.resolve(), self.release)
        snapshot, = deploy.STATE.glob('before-*')
        self.assertEqual((snapshot / 'data/homeatlas.db').read_bytes(), b'old schema')
        self.assertEqual(self.commands[0][1], 'stop')
        self.assertTrue((deploy.DATA / 'plugins/documents/package.zip').is_file())

    def test_failed_health_restores_database_secrets_plugin_and_binary(self):
        installed = deploy.DATA / 'plugins/documents'
        installed.mkdir(parents=True)
        (installed / 'package.zip').write_bytes(b'old package')
        self.health.side_effect = [False, True]
        with self.assertRaisesRegex(RuntimeError, 'failed /api/health'):
            deploy.deploy(self.release, self.digests)
        self.assertEqual(deploy.CURRENT.resolve(), self.old)
        self.assertEqual((deploy.DATA / 'homeatlas.db').read_bytes(), b'old schema')
        self.assertEqual((deploy.DATA / 'setup-code').read_bytes(), b'private setup code')
        self.assertEqual((installed / 'package.zip').read_bytes(), b'old package')

    def test_rejected_plugin_rolls_back_before_starting_new_binary(self):
        self.plugin_error = True
        with self.assertRaisesRegex(RuntimeError, 'plugin rejected'):
            deploy.deploy(self.release, self.digests)
        self.assertEqual(deploy.CURRENT.resolve(), self.old)
        self.assertEqual((deploy.DATA / 'homeatlas.db').read_bytes(), b'old schema')

    def test_unchanged_plugin_skips_install(self):
        installed = deploy.DATA / 'plugins/documents'
        installed.mkdir(parents=True)
        shutil.copy2(self.release / 'documents.hap', installed / 'package.zip')
        deploy.deploy(self.release, self.digests)
        self.assertFalse(any(command[0] == '/usr/sbin/runuser' for command in self.commands))

    def test_snapshot_failure_restarts_old_service_without_changing_data(self):
        self.snapshot_error = True
        with self.assertRaisesRegex(OSError, 'disk full'):
            deploy.deploy(self.release, self.digests)
        self.assertEqual(deploy.CURRENT.resolve(), self.old)
        self.assertEqual(self.commands[-1][1], 'start')
        self.assertEqual((deploy.DATA / 'homeatlas.db').read_bytes(), b'old schema')

    def test_first_install_failure_leaves_no_current_release(self):
        deploy.CURRENT.unlink()
        self.health.return_value = False
        with self.assertRaisesRegex(RuntimeError, 'failed /api/health'):
            deploy.deploy(self.release, self.digests)
        self.assertFalse(deploy.CURRENT.is_symlink())
        self.assertEqual((deploy.DATA / 'homeatlas.db').read_bytes(), b'old schema')

    def test_first_install_success(self):
        deploy.CURRENT.unlink()
        deploy.deploy(self.release, self.digests)
        self.assertEqual(deploy.CURRENT.resolve(), self.release)


class HealthTests(unittest.TestCase):
    def test_accepts_generic_https_origins_and_optional_ports(self):
        for origin, host in [('https://homeatlas.example', 'homeatlas.example'),
                             ('https://HomeAtlas.Example:443/', 'homeatlas.example'),
                             ('https://homeatlas.example:9443/', 'homeatlas.example:9443'),
                             ('https://192.0.2.1', '192.0.2.1'),
                             ('https://[::1]:8443', '[::1]:8443'),
                             ('https://labs.example.ts.net:8443', 'labs.example.ts.net:8443')]:
            self.assertEqual(deploy.validate_origin(origin), host)

    def test_rejects_unsafe_or_invalid_origins(self):
        for origin in ['http://homeatlas.example', 'https://user:secret@homeatlas.example',
                       'https://homeatlas.example/path', 'https://homeatlas.example?token=x',
                       'https://homeatlas.example#x', 'https://homeatlas.example:0',
                       'https://homeatlas.example:65536', 'https://homeatlas.example:bad', 'https://homeatlas.example:',
                       'https://homeatlas.example\n', 'https://[invalid]', 'https://']:
            with self.subTest(origin=origin), self.assertRaises(ValueError):
                deploy.validate_origin(origin)

    def test_health_uses_loopback_with_configured_public_host(self):
        with patch.object(deploy.urllib.request, 'build_opener') as build:
            build.return_value.open.return_value.__enter__.return_value.status = 200
            self.assertTrue(deploy.healthy('labs.example.ts.net:8443'))
            request = build.return_value.open.call_args.args[0]
            self.assertEqual(request.full_url, 'http://127.0.0.1:8090/api/health')
            self.assertEqual(request.get_header('Host'), 'labs.example.ts.net:8443')

    def test_reads_quoted_systemd_origin_and_rejects_invalid_configuration(self):
        with tempfile.TemporaryDirectory() as folder:
            config = Path(folder) / 'homeatlas.env'
            with patch.object(deploy, 'ENV_FILE', config):
                config.write_text('SMTP_PASSWORD=private\nHOMEATLAS_PUBLIC_URL="https://labs.example.ts.net:8443"\n')
                self.assertEqual(deploy.public_host(), 'labs.example.ts.net:8443')
                for value in ('http://labs.example.ts.net:8443', '', 'https://labs.example.ts.net:8443/extra'):
                    config.write_text('HOMEATLAS_PUBLIC_URL=' + value)
                    with self.assertRaises(ValueError):
                        deploy.public_host()


if __name__ == '__main__':
    unittest.main()
