#!/usr/bin/env python3
"""Collect retained license notices for the built release, without new dependencies."""
import json
from pathlib import Path
import subprocess
import sys
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True)


def json_objects(raw):
    decoder = json.JSONDecoder()
    while raw.strip():
        value, end = decoder.raw_decode(raw.lstrip())
        yield value
        raw = raw.lstrip()[end:]


def notices(directory):
    return sorted(p for p in directory.iterdir() if p.is_file()
                  and p.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'NOTICE', 'AUTHORS', 'THIRD-PARTY', 'THIRD_PARTY')))


def main():
    if len(sys.argv) != 2:
        raise SystemExit('usage: package-notices.py output.zip')
    files = {name: ROOT / name for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.md')}
    files['components/LICENSE.shadcn-vue'] = ROOT / 'apps/web/src/components/ui/LICENSE.shadcn-vue'
    for p in (ROOT / 'docs/licenses').iterdir():
        files['fonts/' + p.name] = p
    modules = {}
    for dependency in json_objects(run('go', 'list', '-deps', '-json', '-tags', 'production', './cmd/homeatlas')):
        module = dependency.get('Module', {})
        if module and not module.get('Main'):
            modules[module['Path'] + '@' + module['Version']] = Path(module['Dir'])
    for name, directory in modules.items():
        found = notices(directory)
        if not found:
            raise RuntimeError('Missing upstream Go notice: ' + name)
        files.update({'go/' + name + '/' + p.name: p for p in found})
    goroot = Path(run('go', 'env', 'GOROOT').strip())
    standard_license = goroot / 'LICENSE'
    if not standard_license.is_file():
        standard_license = goroot.parent / 'LICENSE'
    files['go/standard-library/LICENSE'] = standard_license
    if (goroot / 'PATENTS').is_file():
        files['go/standard-library/PATENTS'] = goroot / 'PATENTS'
    web = ROOT / 'apps/web'
    for name in run('npm', '--prefix', str(web), 'ls', '--omit=dev', '--all', '--parseable').splitlines():
        directory = Path(name)
        if directory == web:
            continue
        package = json.loads((directory / 'package.json').read_text())
        found = notices(directory)
        # Platform bindings share their matching Rolldown distribution notices.
        if not found and package['name'].startswith('@rolldown/binding-'):
            parent = web / 'node_modules/rolldown'
            upstream = json.loads((parent / 'package.json').read_text())
            if package.get('repository') == upstream.get('repository') and package['version'] == upstream['version']:
                found = notices(parent)
        if not found:
            raise RuntimeError('Missing upstream web notice: ' + package['name'])
        prefix = 'npm/' + package['name'] + '@' + package['version'] + '/'
        files.update({prefix + p.name: p for p in found})
    with zipfile.ZipFile(sys.argv[1], 'x', compression=zipfile.ZIP_DEFLATED, strict_timestamps=False) as archive:
        for name, source in sorted(files.items()):
            archive.write(source, name)
    print(f'Packaged {len(files)} project and dependency notices in {sys.argv[1]}')


if __name__ == '__main__':
    main()
