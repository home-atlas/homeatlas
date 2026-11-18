# Recovery after an interrupted operation

HomeAtlas acquires an exclusive `.homeatlas.lock` in its private data directory before startup maintenance. The operating system releases the lock on exit, including SIGKILL. A second server fails before cleaning anything. Do not remove or replace the lock file while a server runs. Use one process on a local filesystem with working advisory locks; this is not multi-node coordination. Stop the server before package installation or filesystem maintenance.

## What happens on restart

Before HTTP and scheduled workers start, HomeAtlas:

1. Removes abandoned `.household-backup-*` workspaces, including received/decrypted archives and temporary SQLite snapshots, and `file-staging/original-*` downloads. These are working files, not retained backups. Unrelated paths are preserved; symlink work directories are rejected.
2. Marks interrupted scheduled-backup entries `failed` / `backup_interrupted` and removes their incomplete publications. Previous successful archives, manual downloads and pre-restore safety copies are preserved.
3. Reads local-original references for all households of each installed collection, including disabled plugins. Unreferenced regular files with valid original keys move to `<collection-directory>/.recovered/<batch>/<original-key>`. Referenced originals, pending local source deletions, unknown files and directories remain untouched. Missing schemas or removed/unknown packages are not interpreted as empty collections.
4. Retries durable storage cleanup intents within a ten-second startup budget, up to 256 tasks. Unavailable remote storage leaves tasks queued for another restart or the existing storage-move flow, without preventing startup.

Originals are quarantined rather than deleted: some are intentionally retained damaged files from degraded pre-restore backups. Batches are private, are not served and have no automatic expiry. Inspect/remove them only with the server stopped and after preserving needed files. They are not included in household exports; preserve them separately if needed. Retain approved `.hap` packages and deployment configuration for fresh-install recovery as before.

## Atomicity and remote storage

Upload and restore sync staged originals before publishing references. A kill before commit leaves the old database state; a kill after commit leaves the new state. Reconciliation preserves referenced files and quarantines local leftovers. Current membership, capabilities and grants still govern access; restoring does not re-grant revoked permissions.

New S3 uploads/moves record a cleanup intent in the existing SQLite queue **before PUT**. Publishing the locator removes the intent in the collection transaction. Failed/killed operations retain it; cleanup checks committed locators before deletion. New uploads cannot reuse published object keys. Old untracked bucket objects are not discovered/deleted; HomeAtlas does not enumerate arbitrary bucket contents. Connection probes are not covered by upload intents.

No schema migration, backup-format or plugin SDK/ABI change is required. The lock is runtime infrastructure, not part of a household export. Restore remains exclusive.

## Verification

On Linux/macOS, `TestPersistentPluginCrashRecovery` kills child processes during upload, snapshot export, encryption, scheduled publication, decryption, restore staging, an uncommitted restore transaction, and after commit (also with a damaged old original). It restarts the normal app and verifies SQLite integrity/foreign keys, original contents, a second household, revoked permissions, authenticated access, the last good encrypted archive and reconciliation. `TestRemoteUploadSIGKILLRecoversDurableIntent` kills an uploader after PUT to a private TLS S3 fixture.

```sh
go test ./internal/app -run '^TestPersistentPluginCrashRecovery$' -count=1 -v
go test -race ./internal/core/recovery ./internal/core/objectstore/... -count=1
make test
```

Checks use synthetic accounts/directories and test-only checkpoints; production has no crash switches. The Documents/WASI test runs natively, including the critical release profile. This proves abrupt process termination, not physical power loss, disk corruption, a commercial S3 service or an arbitrarily delayed remote write completing after cleanup. No physical cut is performed against the user's lab.
