#!/usr/bin/env python3
"""Verify the released Core/package boundary against disposable, private data."""
import argparse
import base64
import hashlib
import http.cookiejar
import json
import pathlib
import resource
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=pathlib.Path)
    parser.add_argument('--package', required=True, type=pathlib.Path)
    args = parser.parse_args()
    binary, package = args.binary.resolve(), args.package.resolve()
    before, package_hash = sha(binary), sha(package)
    with tempfile.TemporaryDirectory(prefix='homeatlas-documents-plugin-') as directory:
        root = pathlib.Path(directory)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        origin = f'http://127.0.0.1:{port}'
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        process = None
        log = (root / 'server.log').open('wb')

        def request(path, method='GET', body=None, headers=None):
            request_headers = {'Origin': origin, **(headers or {})}
            if isinstance(body, dict):
                body = json.dumps(body).encode()
                request_headers['Content-Type'] = 'application/json'
            req = urllib.request.Request(origin + path, body, request_headers, method=method)
            try:
                with opener.open(req, timeout=120) as response:
                    return response.status, response.read()
            except urllib.error.HTTPError as error:
                return error.code, error.read()

        def stop():
            nonlocal process
            if process is not None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise RuntimeError('server did not shut down')
                process = None

        def start(disabled=False):
            nonlocal process
            flags = [str(binary), '-data', str(root / 'data'), '-addr', f'127.0.0.1:{port}', '-public-url', origin]
            if disabled:
                flags += ['-plugins', 'none']
            process = subprocess.Popen(flags, stdout=log, stderr=log)
            for _ in range(100):
                if process.poll() is not None:
                    raise RuntimeError('server exited before readiness')
                try:
                    if request('/api/health')[0] == 200:
                        return
                except (urllib.error.URLError, TimeoutError):
                    pass
                time.sleep(0.05)
            raise RuntimeError('server did not become ready')

        try:
            start()
            assert request('/api/setup', 'POST', {
                'code': (root / 'data/setup-code').read_text().strip(),
                'name': 'Test owner', 'username': 'external-owner', 'password': secrets.token_urlsafe(24),
                'householdName': 'Disposable integration home', 'timezone': 'UTC',
            })[0] == 201
            assert request('/api/modules') == (200, b'[]\n')
            assert request('/api/documents')[0] == 404
            stop()
            installed = subprocess.run([
                str(binary), 'plugins', 'install', '-data', str(root / 'data'), '-file', str(package),
                '-sha256', package_hash, '-allow',
                'documents.read,documents.write,documents.share,documents.files,documents.collection',
            ], check=True, capture_output=True, text=True)
            assert 'Installed documents' in installed.stdout
            assert sha(binary) == before
            start()
            status, raw = request('/api/modules')
            modules = json.loads(raw)
            assert status == 200 and len(modules) == 1 and modules[0]['runtime'] == 'wasi-preview1'
            boundary = secrets.token_hex(16)
            content = 'Documento externo de prueba'.encode()
            metadata = json.dumps({'title': 'Factura plugin', 'category': 'recibos', 'description': '', 'tags': ['Prueba']})
            body = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n{metadata}\r\n'
                    f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="prueba.txt"\r\nContent-Type: text/plain\r\n\r\n').encode() + content + f'\r\n--{boundary}--\r\n'.encode()
            status, raw = request('/api/documents', 'POST', body, {'Content-Type': f'multipart/form-data; boundary={boundary}'})
            assert status == 201, (status, raw)
            item = json.loads(raw)
            assert request('/api/documents/' + item['id'] + '/download') == (200, content)
            assert item['id'] in request('/api/search?q=Factura')[1].decode()
            status, archive = request('/api/household/backups', 'POST', headers={
                'X-HomeAtlas-Backup-Version': '4',
                'X-HomeAtlas-Backup-Password': base64.b64encode(secrets.token_urlsafe(24).encode()).decode(),
            })
            assert status == 200 and archive.startswith(b'age-encryption.org/v1'), status
            stop()
            start(disabled=True)
            assert request('/api/modules') == (200, b'[]\n')
            assert request('/api/documents')[0] == 404
            stop()
            start()
            assert request('/api/documents/' + item['id'] + '/download') == (200, content)
            assert sha(binary) == before
            stop()
            rss = resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss
            import sys
            print(json.dumps({
                'core_sha256': before, 'package_sha256': package_hash, 'core_unchanged': True,
                'empty_core': True, 'installed_runtime': modules[0]['runtime'], 'search_download_restart': True,
                'encrypted_backup_bytes': len(archive), 'peak_child_rss_mib': round(rss / (1048576 if sys.platform == 'darwin' else 1024), 2),
                'scope': 'small disposable local run; no cgroup limit or prolonged-load certification',
            }, indent=2))
        finally:
            stop()
            log.close()


if __name__ == '__main__':
    main()
