#!/usr/bin/env python3
"""Package a separately built WASI command; no server rebuild is involved."""
import hashlib
import json
import pathlib
import sys
import zipfile


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: package-plugin.py manifest.json plugin.wasm output.hap")
    manifest = json.loads(pathlib.Path(sys.argv[1]).read_text())
    wasm = pathlib.Path(sys.argv[2]).read_bytes()
    if not wasm.startswith(b"\x00asm\x01\x00\x00\x00") or len(wasm) > 8 * 1024 * 1024:
        raise SystemExit("expected a WebAssembly v1 command no larger than 8 MiB")
    manifest["wasmSHA256"] = hashlib.sha256(wasm).hexdigest()
    out = pathlib.Path(sys.argv[3])
    with zipfile.ZipFile(out, "x", compression=zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("manifest.json", json.dumps(manifest, ensure_ascii=False))
        archive.writestr("plugin.wasm", wasm)
    print(f"SHA-256: {hashlib.sha256(out.read_bytes()).hexdigest()}")
    permissions = [manifest['id'] + '.read', manifest['id'] + '.write']
    if manifest.get('storage') in ('user', 'shared'):
        permissions.append(manifest['id'] + '.storage')
    if manifest.get('collection'):
        permissions.extend([manifest['id'] + '.share', manifest['id'] + '.files', manifest['id'] + '.collection'])
    if manifest.get('storage') == 'shared':
        permissions.extend([manifest['id'] + '.share', manifest['id'] + '.storage.shared'])
    print(f"Permissions: {','.join(permissions)}")


if __name__ == "__main__":
    main()
