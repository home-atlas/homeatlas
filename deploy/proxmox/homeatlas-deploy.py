#!/usr/bin/python3 -I
"""Receive a release tar.gz on stdin; install as homeatlas, never execute it as root."""
import fcntl
import grp
import hashlib
import os
from pathlib import Path
import re
import shutil
import shlex
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request

RELEASES = Path('/opt/homeatlas/releases')
CURRENT = Path('/opt/homeatlas/current')
DATA = Path('/var/lib/homeatlas')
STATE = Path('/var/lib/homeatlas-deploy')
ENV_FILE = Path('/etc/homeatlas/homeatlas.env')
PERMISSIONS = 'documents.read,documents.write,documents.share,documents.files,documents.collection'
LIMITS = {'homeatlas': 256 << 20, 'documents.hap': 10 << 20, 'SHA256SUMS': 4096}


def receive(stream, stage):
    seen = set()
    # Never extract archive paths, links, ownership or modes supplied by the sender.
    with tarfile.open(fileobj=stream, mode='r|gz') as archive:
        for entry in archive:
            if (entry.name not in LIMITS or entry.name in seen or not entry.isfile()
                    or entry.issparse() or not 0 < entry.size <= LIMITS[entry.name]):
                raise ValueError('Release must contain only three bounded, regular assets')
            seen.add(entry.name)
            with archive.extractfile(entry) as source, (stage / entry.name).open('xb') as target:
                shutil.copyfileobj(source, target)
    if seen != set(LIMITS):
        raise ValueError('Missing release assets')
    expected = {}
    for line in (stage / 'SHA256SUMS').read_text(encoding='ascii').splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  (homeatlas|documents\.hap)', line)
        if not match or match[2] in expected:
            raise ValueError('Invalid checksum manifest')
        expected[match[2]] = match[1]
    if set(expected) != {'homeatlas', 'documents.hap'}:
        raise ValueError('Missing checksums')
    for name, digest in expected.items():
        with (stage / name).open('rb') as source:
            if hashlib.file_digest(source, 'sha256').hexdigest() != digest:
                raise ValueError(f'Checksum mismatch: {name}')
    return expected


def run(*args):
    return subprocess.run(args, check=True, stdin=subprocess.DEVNULL, timeout=300)


def switch(target):
    temporary = CURRENT.with_name('.current-new')
    temporary.unlink(missing_ok=True)
    temporary.symlink_to(target)
    os.replace(temporary, CURRENT)


def public_host():
    value = None
    for line in ENV_FILE.read_text().splitlines():
        if line.strip().startswith('HOMEATLAS_PUBLIC_URL='):
            words = shlex.split(line.strip().split('=', 1)[1], comments=True)
            if len(words) != 1:
                raise ValueError('Invalid HOMEATLAS_PUBLIC_URL')
            value = words[0]
    if value is None or not re.fullmatch(r'https://[a-zA-Z0-9][a-zA-Z0-9.-]*\.ts\.net:8443', value):
        raise ValueError('Configure HOMEATLAS_PUBLIC_URL as https://VM.TAILNET.ts.net:8443')
    return value.removeprefix('https://')


def healthy(host):
    # Ignore proxy environment variables; health must reach this VM's loopback.
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    request = urllib.request.Request('http://127.0.0.1:8090/api/health', headers={'Host': host})
    for _ in range(30):
        try:
            with client.open(request, timeout=2) as response:
                if response.status == 200:
                    return True
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(1)
    return False


def deploy(release, digests):
    host = public_host()
    if CURRENT.exists() and not CURRENT.is_symlink():
        raise RuntimeError('current must be a managed symlink')
    previous = os.readlink(CURRENT) if CURRENT.is_symlink() else None
    active = subprocess.run(['/usr/bin/systemctl', 'is-active', '--quiet', 'homeatlas.service'],
                            stdin=subprocess.DEVNULL, check=False).returncode == 0
    snapshot = Path(tempfile.mkdtemp(prefix='before-', dir=STATE))
    snapshot.chmod(0o700)
    (snapshot / 'previous-release').write_text((previous or '') + '\n')
    (snapshot / 'previous-active').write_text(str(active) + '\n')
    print(f'Pre-deploy state: {snapshot}', flush=True)
    run('/usr/bin/systemctl', 'stop', 'homeatlas.service')
    try:
        # Stop first: SQLite, WAL, local originals, keys and package receipts stay consistent.
        run('/bin/cp', '-a', str(DATA), str(snapshot / 'data'))
        (snapshot / 'snapshot-complete').touch()
    except BaseException:
        if active:
            run('/usr/bin/systemctl', 'start', 'homeatlas.service')
        raise

    try:
        installed = DATA / 'plugins/documents'
        package = installed / 'package.zip'
        old_digest = None
        if package.is_file():
            with package.open('rb') as source:
                old_digest = hashlib.file_digest(source, 'sha256').hexdigest()
        if old_digest != digests['documents.hap']:
            if installed.exists():
                shutil.rmtree(installed)
            run('/usr/sbin/runuser', '-u', 'homeatlas', '--', str(release / 'homeatlas'),
                'plugins', 'install', '-data', str(DATA), '-file', str(release / 'documents.hap'),
                '-sha256', digests['documents.hap'], '-allow', PERMISSIONS)
        switch(release)
        run('/usr/bin/systemctl', 'start', 'homeatlas.service')
        if not healthy(host):
            raise RuntimeError('New release failed /api/health')
    except BaseException:
        print(f'Deployment failed; restoring {snapshot}', file=sys.stderr, flush=True)
        # If stopping fails, leave the snapshot intact for manual recovery, never copy live DBs.
        run('/usr/bin/systemctl', 'stop', 'homeatlas.service')
        shutil.rmtree(DATA)
        run('/bin/cp', '-a', str(snapshot / 'data'), str(DATA))
        if previous is None:
            CURRENT.unlink(missing_ok=True)
        else:
            switch(previous)
            if active:
                run('/usr/bin/systemctl', 'start', 'homeatlas.service')
                if not healthy(host):
                    raise RuntimeError(f'Rollback health check failed; recover from {snapshot}')
        raise
    print(f'Deployed {release.name}; retained {snapshot}', flush=True)


def interrupted(signum, _frame):
    raise RuntimeError(f'Deployment interrupted by signal {signum}')


def main():
    if os.geteuid() != 0 or len(sys.argv) != 1:
        raise RuntimeError('Run as root with no arguments; provide release tar.gz on stdin')
    os.environ.clear()
    os.environ.update(PATH='/usr/sbin:/usr/bin:/sbin:/bin', LANG='C.UTF-8')
    os.chdir('/')
    os.umask(0o077)
    for signum in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(signum, interrupted)
    with (STATE / 'deploy.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        with tempfile.TemporaryDirectory(prefix='.stage-', dir=RELEASES) as directory:
            stage = Path(directory)
            digests = receive(sys.stdin.buffer, stage)
            group = grp.getgrnam('homeatlas').gr_gid
            for path in [stage, *stage.iterdir()]:
                os.chown(path, 0, group)
                path.chmod(0o750 if path == stage or path.name == 'homeatlas' else 0o640)
            release = RELEASES / (time.strftime('%Y%m%dT%H%M%SZ', time.gmtime()) + '-' + stage.name[7:])
            stage.rename(release)
            deploy(release, digests)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(f'homeatlas-deploy: {error}', file=sys.stderr)
        sys.exit(1)
