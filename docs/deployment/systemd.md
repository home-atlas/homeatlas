# Generic systemd installation

This recipe targets Debian/Ubuntu Linux amd64 with systemd and Python 3.11+, `sudo`/`visudo` and `runuser`. The current automated executable is Linux amd64. HomeAtlas itself can also be built from source and run without this installer.

Choose your browser origin and configure DNS/TLS through a reverse proxy. Proxmox and Tailscale are optional.

```sh
sudo bash deploy/systemd/install.sh https://homeatlas.example
```

An explicit public HTTPS port is supported, such as `https://homeatlas.example:9443`. Credentials, paths, query strings, fragments and plain HTTP origins are rejected. The installer creates a private service account, directories, a hardened systemd service and the root-owned release receiver. It does not start an application until the first release is deployed.

| Path | Purpose |
| --- | --- |
| `/etc/homeatlas/homeatlas.env` | Root-only origin and optional SMTP configuration |
| `/var/lib/homeatlas` | Private persistent database, originals, keys and installed packages |
| `/opt/homeatlas/releases` | Root-owned binaries/packages |
| `/opt/homeatlas/current` | Atomic symlink to the active release |
| `/var/lib/homeatlas-deploy` | Root-only deployment snapshots |
| `/usr/local/sbin/homeatlas-deploy` | Fixed receiver, no arguments |

HomeAtlas listens on `127.0.0.1:8090`. Proxy that listener over HTTPS and preserve the public Host header. The deployment health check reaches loopback directly with the configured public Host; it does not depend on DNS/TLS reaching this machine from itself. Configure the proxy before clients access the app.

## Deploy a validated release

Obtain `homeatlas`, `documents.hap` and `SHA256SUMS` from one release. Retain its accompanying license notices. Verify checksums before packaging the three runtime assets:

```sh
(cd dist && sha256sum --check --strict SHA256SUMS)
tar -czf deploy.tgz -C dist homeatlas documents.hap SHA256SUMS
sudo /usr/local/sbin/homeatlas-deploy < deploy.tgz
```

The receiver rejects additional files, paths, symlinks, duplicate entries and oversized or corrupt assets. It runs the application and installer as `homeatlas`, never as root. It stops the service before copying data, installs the approved Documents package when its digest changes, switches releases and checks health.

Initial setup code: `sudo cat /var/lib/homeatlas/setup-code`. Create the owner at your HTTPS origin. Subsequent members join by invitation.

## Optional restricted deployment account

Add `--with-deploy-user` only when remote automation needs a dedicated account:

```sh
sudo bash deploy/systemd/install.sh https://homeatlas.example --with-deploy-user
```

This creates `deploy` and grants passwordless sudo for exactly the no-argument receiver. Existing broader administrator membership is rejected. Generic installation does not create that account or change its sudo rule. Re-running the installer preserves the existing environment file; review that origin separately. Existing deployment authorization is preserved when omitting the option.

## Backups and rollback

Startup/health failure automatically restores the previous executable and the stopped local data snapshot. The snapshot includes database/WAL, local originals, keys and package approvals; remote S3 objects are not copied. Snapshots contain plaintext secrets and data, are root-only, stay on this VM and need an operator retention policy. Never copy a running SQLite data directory for recovery.

Keep password-encrypted application backups and exact `.hap` packages off-machine. Recover a household through a clean installation with those packages present; SMTP/S3 credentials, sessions and device PIN bindings need separate configuration. Rollback snapshots and encrypted application backups serve different purposes.

Updating an existing installation from the former lab layout: run this installer from the new `deploy/systemd` directory with the same origin and `--with-deploy-user` if remote deployment is enabled. Installed filesystem paths, service/data ownership and CI's fixed sudo command remain compatible.
