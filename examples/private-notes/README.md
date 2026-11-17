# Private notes SDK example

A separately compiled WASI command using `sdk/pluginstorage`, with save/read/list/delete declarative views. It imports no host `internal` package and receives acting identity from the host.

From the repository root:

```sh
mkdir -p bin/plugins
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/plugins/private-notes.wasm ./examples/private-notes
python3 scripts/package-plugin.py examples/private-notes/manifest.json bin/plugins/private-notes.wasm bin/plugins/private-notes.hap
```

Use a new output path if the `.hap` exists. The packager prints the digest. Stop the server and install with that digest:

```sh
./bin/homeatlas plugins install -data ./data -file ./bin/plugins/private-notes.hap -sha256 PACKAGE_SHA256 -allow ext-private-notes.read,ext-private-notes.write,ext-private-notes.storage
./bin/homeatlas
```

Activate the module under **Mis módulos**. Grant read/write capabilities to the intended members. Identical keys belong to each member's private namespace; `storage` is installation consent, not an account capability. Removing and reinviting a member does not revive the old private namespace. The household owner can include private notes in a full backup.

The critical native integration test compiles this directory, installs the package, executes storage calls and confirms persistence after restart. Compatibility fixtures remain separate. See [package contract](../../docs/plugins/README.md) and [storage semantics](../../docs/plugins/storage.md).
