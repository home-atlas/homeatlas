# Security policy

HomeAtlas is experimental. Security fixes target current `main`; there are no separately maintained stable release branches yet. Support is best effort during development.

## Report a vulnerability privately

Use [GitHub private vulnerability reporting](https://github.com/home-atlas/homeatlas/security/advisories/new). Include the affected commit/release, reproduction using synthetic data, expected behavior, actual impact and relevant sanitized logs. Coordinate disclosure in that private report.

Household documents, passwords, session cookies, setup/recovery codes, SMTP/S3 credentials and backup passwords must stay out of reports. Ordinary bugs without security impact can use public issues.

## Deployment assumptions

Run one process per private data directory. Serve HTTPS outside loopback and configure the exact public origin; preserve the Host header at the reverse proxy. Keep encrypted backups and exact plugin packages outside the server. See the [trust model and limits](docs/architecture/README.md) and [installation guide](docs/deployment/systemd.md).
