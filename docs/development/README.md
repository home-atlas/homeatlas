# Development

Requirements: Go 1.27.1+, Node.js 24+, npm, Make and Python 3. Use a current supported Python; the systemd tools require 3.11+. Clone the repository, run `make install`, then `make dev-api` and `make dev-web` in separate terminals. Open `http://127.0.0.1:5173`. Pass the same `WEB_PORT` to both commands if changing it.

`make build` embeds the web build into `bin/homeatlas`. Runtime configuration comes from flags and process environment; `.env.example` is a reference and `.env` is not loaded automatically. Initial setup uses the private `data/setup-code` file; there are no default credentials.

## Checks

| Command | Coverage |
| --- | --- |
| `make test-ci` | Architecture boundaries, core race units, selected security/concurrency cases, real critical data/plugin flows and vet |
| `make test-web` | All frontend tests, type checking and production web build |
| `make test-deploy` | Release validation, configuration, data copy and rollback tests |
| `make test-go` | Exhaustive Go race suite, legacy fixtures and vet |
| `make test-wasi` | Native integration with real WASI packages, forced uncached |
| `make test` | All exhaustive checks above |

Pull requests run the three critical validation jobs. Merges into main run those same checks, package a development release and optionally deploy. [The exhaustive workflow](../../.github/workflows/full-tests.yml) is manually dispatched; security or recovery changes require it before merge. It may take around 15–20 minutes depending on the runner. Production password hashing and encryption costs remain enabled in tests.

Real Go-WASI integration tests intentionally use native execution because race instrumentation exceeds the production cold-runtime budget; small guest fixtures still exercise runtime/storage boundaries under race. The public SDK example is compiled, installed, executed and restarted in `TestPersistentPluginPublicSDKExample`.

`TestPersistentPluginCrashRecovery` adds native SIGKILL checkpoints for upload, backup and pre/post-commit restore, also in critical CI on Unix. Core recovery and remote-intent checks run with race. `TestSecondInstanceCannotCleanActiveWorkspace` checks the lock before cleanup. See [crash recovery](../deployment/crash-recovery.md) for preservation and test limits.

Use temporary data and synthetic accounts. Test backups, restores, grants/revocation and restart when changing those paths; preserve compatibility fixtures. Do not lower resource, password or encryption limits to speed up CI. See [CONTRIBUTING](../../CONTRIBUTING.md).

## Persistence changes

Keep SQL calls, scans and migrations in each feature’s `adapters/sqlite` package. The existing host snapshot infrastructure in `internal/app/*_sql.go` remains an explicit technical boundary. The application consumes typed records and `unitofwork.Transaction`; never pass query strings or driver rows through a repository port. HTTP identity errors map from `fault` classifications in the transport. Preserve the exact migration SQL bytes and existing backup tables/versions when moving code.

`TestLayerDependencies` and `TestPersistenceBoundaries` run in critical CI. The first checks transitive Go dependencies: domain/application cannot reach HTTP, SQL, AWS, wazero or adapters, even through another host package. Feature root facades belong to composition and must not be imported by the inner packages. `identity/application.TestIdentityApplicationPreservesMemberTransaction` runs the application against a non-SQL repository and checks revocation, protected accounts and rollback on write/commit failure. Existing integration tests exercise actual SQLite rollback across modules, fresh-install recovery, Local/S3 moves and private/shared guest storage. Use those tests when changing transaction scopes.

Domain/application unit tests and every feature adapter run in the critical profile. Exhaustive HTTP identity tests remain in the manual suite, with the selected critical flows still automatic. WASI cache/admission tests live beside the WASI adapter. Wire-contract and JSON tests protect the unchanged guest ABI and private persistence fields.
