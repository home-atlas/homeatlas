# Experimental plugin contract v1

A plugin is a locally approved WASI Preview 1 command. It ships declarative data and WebAssembly, not third-party browser JavaScript. Start with the [private-notes example](../../examples/private-notes/README.md) or the official [Documents manifest](../../plugins/documents/manifest.json).

## Package and approval

A `.hap` is a ZIP containing exactly two regular files at its root: `manifest.json` (up to 32 KiB) and `plugin.wasm` (up to 8 MiB). The complete archive is at most 10 MiB. Extra names, directories, duplicates, paths, invalid JSON and unknown manifest fields are rejected. The packager fills `wasmSHA256`; the CLI also requires the SHA-256 of the entire `.hap` and an exact capability approval list. Integrity approval does not authenticate a publisher.

Stop HomeAtlas before CLI installation. An existing plugin ID cannot be overwritten by `plugins install`; managed update/uninstall and hot loading are not implemented. The host validates packages and approval receipts on each startup. Keep exact approved packages for backup recovery, and restart after installing a new one. On first startup with a newly installed package, the application seeds its declared account capabilities for household owners according to installation policy. Other members need explicit grants; restarting does not reapply a revoked grant.

## Manifest

| Field | Contract |
| --- | --- |
| `contractVersion` | Exact string `"1"` |
| `id` | Lowercase letter followed by lowercase letters, digits or hyphens, at most 48 characters; Core namespaces are reserved by `modules.ValidID` |
| `name`, `version` | Nonblank bounded strings, at most 100 and 40 bytes respectively |
| `wasmSHA256` | SHA-256 of `plugin.wasm`, filled by the packager |
| `storage` | Absent, `"user"` or `"shared"`; cannot be combined with a file collection |
| `views` | 1–8 unique views, including `validate` for a collection |
| `collection` | Optional [host-controlled file collection definition](collections.md) |

Non-collection IDs must start with `ext-`. Each view has `id`, `title`, `description`, `submitLabel`, `fields`, optional `readOnly` and optional `permission`. IDs/field names match `[a-z][a-z0-9_]{0,47}`. Titles are at most 120 bytes, descriptions 1,000 and submit labels 80. A view declares up to 12 unique fields with `name`, `label`, `type` (`text`, `number`, `textarea`) and `required`. Field labels are at most 120 bytes. Host and renderer control routes, markup, styles and resource authorization.

## Invocation and output

The host invokes a fresh `_start` instance with JSON on stdin:

```json
{"view":"save","input":{"key":"manual","value":"Check the boiler yearly"}}
```

Write one JSON object to stdout and exit successfully:

```json
{"text":"Saved."}
```

Output is plain text, not HTML or executable code. Ordinary output is bounded at 16 KiB with a nonblank `text` of at most 12,000 bytes. Collection validation permits a 32 KiB outer envelope with at most 16 KiB of decoded metadata text.

HTTP actions accept only declared string-valued fields, with a 16 KiB JSON request and 2,000 bytes per field. Numeric fields must parse as finite numbers between -1e15 and 1e15. Unknown fields are rejected. `readOnly` selects read capability and prohibits guest storage writes. Ordinary mutations require write; `permission:"share"` is available only to shared-storage views and requires sharing capability. Core host checks still apply to every operation.

## Capabilities

| Feature | Exact installation approval entries |
| --- | --- |
| Every package | `<id>.read`, `<id>.write` |
| Private storage | Plus `<id>.storage` |
| Shared storage | Plus `<id>.share`, `<id>.storage`, `<id>.storage.shared` |
| File collection | Plus `<id>.share`, `<id>.files`, `<id>.collection` |

Storage/files/collection entries consent to host facilities at installation. Account capabilities and per-resource grants still control what a particular member can do. Use explicit approval rather than accepting every capability printed by an unreviewed package.

Only WASI imports and the consented `homeatlas_storage_v1.call` import are available. No host `internal` imports, mounted filesystem, SQL, network, environment or credentials are exposed. See [runtime limits](../architecture/README.md#operating-limits) and [storage semantics](storage.md).

## Compatibility and backup

The ABI is experimental. Pin the host, contract and exact package digest; follow migration notes before updating. Full household backups include private/shared plugin data. They do not contain executable `.hap` installers. Restore requires compatible participants and the referenced exact packages; private data is not exposed just because an account administers the installation.
