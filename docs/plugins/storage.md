# Private and shared storage SDK v1

Build a Go guest with `GOOS=wasip1 GOARCH=wasm`. Import `github.com/home-atlas/homeatlas/sdk/pluginstorage`; its WASI build constraint is intentional. The host implements the `homeatlas_storage_v1.call` ABI. [The example](../../examples/private-notes/README.md) builds without importing host `internal` packages.

## Private records

`Get`, `Set`, `Delete` and `List(after, limit)` operate within the current plugin, household and continuous member identity. The guest cannot specify an acting user/household. Identical keys for different members remain isolated; removing and reinviting an account does not restore its historical namespace. `Get` returns a separate found flag so empty values are distinguishable from missing keys.

`List` returns keys plus an opaque continuation value; pass it as `after` on the next call. Keys are 1–128 ASCII characters matching `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`. Values are UTF-8 strings of at most 16 KiB.

## Shared records

A manifest with `storage:"shared"` can use private operations plus `GetShared`, `SetShared`, `DeleteShared`, `ListShared`, `GrantShared`, `RevokeShared` and `ListSharedGrants`.

Shared keys are per plugin/household. The creator owns the record. Explicit read grants permit reading; write grants permit updating. Only the current creator can delete or change grants, and sharing requires the current share capability. Targets need current household membership and relevant plugin capabilities. Administrative status alone does not bypass resource ownership. Removing/rejoining a creator or recipient does not revive historical grants.

`GrantShared(key, userID, permission)` targets another member's user ID with `read` or `write`; it does not impersonate that member. Grant pagination uses continuation values returned by the host. Prefer private records unless sharing is an actual product requirement.

## Commit and failure

Changes are staged for an action and become durable only after a successful guest exit, valid output and final host authorization/revision checks. An error, trap, timeout or stale revision prevents the staged action from publishing. `readOnly` views cannot write. The host may return permission denied, conflict or busy; clients should refresh/retry as appropriate, without treating conflicts as success.

Limits: 64 host calls per action, 128 KiB RPC request/response envelopes and up to 64 keys per page. Listing returns only authorized shared keys. Guests should always check SDK errors and pagination results.

Private and shared data are included in authorized full household backups. The household owner can export that private content. Preserve exact `.hap` packages separately for recovery. See [trust and deployment limits](../architecture/README.md).
