# Architecture decisions

This is the public record of implementation-relevant decisions, curated without personal infrastructure details.

| Decision | Status | Rationale and consequence |
| --- | --- | --- |
| Modular monolith and SQLite | Accepted | A self-hosted household runs one process with local metadata; multi-node operation is outside the current design. |
| Composition in `internal/app` | Accepted | Host modules share identity, transaction and backup coordination; guests do not import host internals. |
| Independent WASI packages | Accepted | Approved `.hap` packages install without rebuilding the host; activation is per household and installation is local/offline with restart. |
| Host-owned file collections | Accepted | Guests validate bounded metadata and provide declarative presentation; the host retains originals, ACL and backup mechanics. |
| Private/shared guest storage | Accepted, experimental ABI v1 | Acting identity comes from the host. Writes and grants publish transactionally after a successful action. |
| Portable encrypted backups | Accepted | Retain exact plugin packages separately. Restored originals are local, even when exported from S3; deployment snapshots are a separate recovery mechanism. |
| Application operations and persistence adapters | Accepted, expanded 2026-11-17 | Identity operations separate typed inputs/results and policy from HTTP and SQL. Collections, registry, storage, schedules and guest storage publish through SQL adapters using a shared driver-independent transaction contract. Snapshot codecs remain SQLite infrastructure. |
| Generic systemd, optional Tailscale | Accepted | The product accepts generic HTTPS origins. Private-network deployment is an opt-in recipe, independent of the hypervisor. |
| Product version in root `VERSION` | Accepted | Main produces development prereleases; frontend/plugin package versions do not define product or lab versions. |
| ADR-009: Apache-2.0 | Accepted 2026-11-16 | Chosen by the project owner for adoption, integrations and the SDK ecosystem. Preserve third-party licenses/notices. |
