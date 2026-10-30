<p align="center">
  <img src="apps/web/src/assets/homeatlas-mark.svg" alt="HomeAtlas app icon" width="96" />
</p>

<h1 align="center">HomeAtlas</h1>

<p align="center">
  A home for your household's documents and everyday knowledge.<br />
  <strong>Self-hosted · Modular · Local by default</strong>
</p>

<p align="center">
  <a href="https://github.com/home-atlas/homeatlas/actions/workflows/ci.yml"><img src="https://github.com/home-atlas/homeatlas/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI status" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.27.1%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.27.1 or later" /></a>
  <a href="apps/web/package.json"><img src="https://img.shields.io/badge/Node.js-24%2B-339933?logo=nodedotjs&logoColor=white" alt="Node.js 24 or later" /></a>
  <a href="#architecture"><img src="https://img.shields.io/badge/Vue-3-4FC08D?logo=vuedotjs&logoColor=white" alt="Vue 3" /></a>
  <a href="#roadmap"><img src="https://img.shields.io/badge/status-experimental-orange" alt="Experimental project" /></a>
</p>

HomeAtlas is a family platform you run on your own server to store documents, find information and share it with the people in your household. It combines a single Go process, a web interface and SQLite, with no mandatory external services.

The core manages accounts, households and permissions. Product features arrive as independent plugins: **Documents** is the first and can be installed without rebuilding the core.

> **Status:** active development. The plugin contract is experimental. The project's license has not yet been finalized; see [License](#license).

## Features

| Feature | Available today |
| --- | --- |
| Documents | Upload PDF, PNG, JPEG and TXT files up to 20 MiB; edit metadata, filter and search by title, description or tags. |
| Family and permissions | Invite members, assign roles and explicitly share document viewing and downloading with specific people. |
| Storage | Choose local disk or an S3-compatible bucket; move existing documents between them from Settings. |
| Backups | Create encrypted backups, schedule them, restore household data or recover a household on a new installation. |
| Access | Sign in with a username or email and password; set up assisted access with a device-bound PIN for eligible accounts. |
| Plugins | Install approved WASI packages through the CLI and restart, without rebuilding the core. |

Search currently covers metadata, not OCR or file contents. AI, inventory and warranty features are on the roadmap.

## Quick start

### Requirements

- **Go 1.27.1 or later**.
- **Node.js 24 or later**, with npm.
- **Make**.
- **Python 3** to package plugins and run their integration tests.

### Build and run

```sh
git clone https://github.com/home-atlas/homeatlas.git
cd homeatlas
make install
make build
./bin/homeatlas
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080).

On first startup, HomeAtlas generates a private setup code in `data/setup-code`. Read it from another terminal on the server:

```sh
cat ./data/setup-code
```

Use it on the setup screen to create the owner account and household. There are no default credentials or open public registration; additional household members join by invitation.

The core starts with an empty module catalog. Install the following plugin to add the document library. The application interface is currently in Spanish; the navigation labels below match it.

## Install Documents

Documents builds into a `.hap` package separately from the server executable.

```sh
make plugin-documents PACKAGE_OUT=bin/plugins/documents.hap
```

The packager prints the package's SHA-256. **Stop the server**, then replace `PACKAGE_SHA256` below with that value:

```sh
./bin/homeatlas plugins install \
  -data ./data \
  -file ./bin/plugins/documents.hap \
  -sha256 PACKAGE_SHA256 \
  -allow documents.read,documents.write,documents.share,documents.files,documents.collection

./bin/homeatlas
```

The package appears under **Mis módulos**. If the output file already exists, choose a different `PACKAGE_OUT` path: the packager does not overwrite packages.

List installed packages with:

```sh
./bin/homeatlas plugins list -data ./data
```

Installing a compatible package requires a restart, but no core rebuild. Manage activation per household in the interface; deactivation preserves data and permissions. Other members receive only the permissions explicitly granted to them.

## Local or S3: your choice

Under **Configuración → Almacenamiento de archivos**, the owner can choose where new uploads are stored:

- **Local**, the default, keeps originals on the server.
- **S3** uses a private bucket, region and credentials. Custom HTTPS endpoints and path-style addressing support compatible providers.

Before enabling S3, HomeAtlas checks write, read and delete access. Credentials are stored encrypted on the server; they are neither passed to the plugin nor returned to the browser.

Changing the destination does not automatically move existing files. To transfer them:

1. Save Local or S3 as the destination.
2. Open **Mover documentos existentes** and review the pending file count and size.
3. Confirm the transfer and keep the tab open for the batch to continue.

Each copy is verified by size and SHA-256 before its location changes and the source is removed. Identifiers, metadata and permissions are preserved. You can stop after the current file and resume later; failed source cleanup remains available for retry.

S3 stores collection originals only. SQLite, installed packages and scheduled backup archives remain on the local server.

## Privacy and recovery

Documents are private to their owner until explicitly shared. Authorized recipients can view and download them, but cannot edit or reshare them. Administrator status does not grant browsing access to every private document.

The owner's full backups **do include private module data for the household**. Backups are password-encrypted and can be downloaded or generated automatically on the server. Keep the password separately and retain copies outside the machine running HomeAtlas.

To recover external modules, also retain the exact `.hap` package: backups contain data, not executable installers. S3 credentials, SMTP configuration, sessions and PIN device bindings are not exported. Configure them again on a new installation.

Backups include originals from local storage and S3. Restoration writes originals locally, so recovery does not need the source provider's credentials. Backup encryption does not automatically encrypt the live database or local originals.

## Architecture

| Layer | Technology |
| --- | --- |
| Server | Go |
| Interface | Vue 3, TypeScript, Vite and shadcn-vue |
| Metadata and search | SQLite with WAL and FTS5 |
| Files | Local disk or S3-compatible storage |
| External plugins | WASI Preview 1, executed with wazero |
| Encrypted backups | age |

The compiled web interface is embedded in the Go executable. Plugins provide a manifest and WASI code; the core supplies shared interface components and authorized storage operations. Guests receive no direct SQL, filesystem, network or credential access, and cannot ship third-party browser JavaScript.

```text
apps/web/             Web interface and shared components
cmd/homeatlas/        Server and CLI entry point
internal/core/        Identity, permissions, modules, storage and backups
internal/app/         HTTP integration and application tests
plugins/documents/    Documents plugin source and manifest
sdk/pluginstorage/    Experimental WASI storage SDK
scripts/              Packaging, verification and measurement tools
```

## Development

Install dependencies with `make install`, then run these commands in two terminals:

```sh
# Terminal 1: API
make dev-api
```

```sh
# Terminal 2: web interface with hot reload
make dev-web
```

Open [http://127.0.0.1:5173](http://127.0.0.1:5173). To use a different frontend port, pass the same `WEB_PORT` to both commands.

### Tests

```sh
make test
```

This runs the exhaustive Go race suite, compatibility fixtures, integration tests with real WASI plugins, static analysis, frontend tests and the web build. Run it before releases and when changing security or recovery behavior.

Pushes and pull requests use a smaller [CI profile](.github/workflows/ci.yml):

- `make test-ci` runs fast core units with race detection; selected login, session, authorization and concurrency tests; and real encrypted-backup, Documents-plugin and Local/S3 flows. The expensive application flows run without race instrumentation.
- `make test-web` keeps all frontend tests and the web build in a separate job.
- The backend job also builds the production executable.

The [Full test suite workflow](.github/workflows/full-tests.yml) is manual: choose **Actions → Full test suite → Run workflow** for exhaustive coverage. Detailed legacy, scheduling, recovery and additional account/plugin scenarios remain there and in `make test`; they are not all repeated on every push.

For local checks, use `make test-ci`, `make test-go`, `make test-wasi` or `make test-web`. Tests keep production password-hashing and backup-encryption costs. The exhaustive race suite can take several minutes, with a configurable 20-minute timeout per test binary (`GO_TEST_TIMEOUT`).

You can also verify independent Documents installation with temporary data:

```sh
python3 scripts/verify-documents-plugin.py \
  --binary ./bin/homeatlas \
  --package ./bin/plugins/documents.hap
```

This checks installation without modifying the executable, upload, search, download, encrypted backup and restart.

## Deployment

The current installation is built from source. Run **one process per data directory** and keep that directory on persistent storage.

To serve the application behind an HTTPS reverse proxy:

```sh
./bin/homeatlas \
  -addr 127.0.0.1:8080 \
  -public-url https://homeatlas.example \
  -data ./data
```

Configure the proxy to preserve the `Host` header. HTTPS is required outside loopback; `-public-url` must match the origin used by the browser.

SMTP is optional and disabled by default. See [.env.example](.env.example): HomeAtlas reads process environment variables and does not load `.env` files automatically.

## Roadmap

- Reproducible distribution and deployment across more environments.
- Evolution and stabilization of the public plugin contract.
- Managed package updates and uninstallation.
- Inventory and warranty modules.
- Optional OCR and AI over authorized information.

The runtime and plugins remain experimental. S3 tests use a local TLS test server; verify connectivity with your provider before using it. Historical memory measurements do not certify every workload or a universal minimum deployment size.

## Contributing

Bug reports, documentation improvements and code contributions are welcome. Include reproduction steps and describe the expected behavior; for larger changes, explain the proposed scope first.

Run `make test` before submitting changes. Preserve data and permission compatibility, add tests for new behavior and use shared interface components. Do not commit credentials, household data, caches or build artifacts.

## License

HomeAtlas's license has not yet been selected. This repository does not currently include a project-level `LICENSE` file.

Dependencies and third-party components retain their own licenses; the shadcn-vue component license is included [alongside its source](apps/web/src/components/ui/LICENSE.shadcn-vue).
