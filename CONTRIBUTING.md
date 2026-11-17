# Contributing to HomeAtlas

Bug reports, documentation, tests and focused pull requests are welcome. Discuss large changes in an issue before implementation. Use fabricated household data in reports and tests.

1. Follow [development setup and checks](docs/development/README.md).
2. Preserve account, household, resource and membership boundaries. Revalidate writes inside the committing transaction.
3. Keep transport parsing separate from application operations; see the [architecture boundaries](docs/architecture/README.md).
4. Add a regression for a bug or a meaningful check for new behavior. Changes to security/recovery require the manual exhaustive suite before merge.
5. Use the shared Vue components for interface changes. Preserve keyboard access, semantic forms and validation.
6. Document changes to contracts, limits, data migrations or installation. Update `VERSION` for a deliberate product version change; frontend package versions are independent.

Contributions intentionally submitted for inclusion are licensed under Apache-2.0, as described in section 5 of [LICENSE](LICENSE). Preserve third-party copyright and license notices. Contributors retain ownership of their contributions.

Report vulnerabilities through the [private channel](SECURITY.md). Keep credentials, household files, database contents, caches and build outputs out of commits.
