# Product versions and releases

The root [`VERSION`](../../VERSION) is the product's base semantic version: `MAJOR.MINOR.PATCH`, without a prefix. It is independent of `apps/web/package.json` and individual plugin manifest versions.

Each successfully validated merge into `main` automatically publishes a **development prerelease** `v<VERSION>-dev.<workflow run number>` on the exact checked commit, for example `v0.1.0-dev.42`. The number can have gaps because PR checks increment it too. Tags do not start another CI pipeline. Development prereleases do not claim stable compatibility.

Change `VERSION` deliberately in a PR. Before 1.0, minor increments may include documented breaking changes and migration/recovery requirements; patches should preserve published behavior and data compatibility. A future stable `vX.Y.Z` publication requires an explicit release process; main does not label every merge stable. No automatic stable publication workflow is supplied yet.

The pipeline publishes its already-built Linux amd64 executable, separately built Documents package and checksums; license notices accompany the release. Critical validation gates publication. It does not rerun the exhaustive suite automatically.

`LAB_DEPLOY_ENABLED=true` at repository level opts into Tailscale deployment after successful publication. Missing/false skips deployment, including in forks. Product tags contain no lab name, address or infrastructure identifier. Existing `-lab.*` tags remain historical releases and valid rollback references.
