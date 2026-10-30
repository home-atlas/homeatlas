WEB_PORT ?= 5173

.PHONY: install dev-api dev-web build test test-go test-wasi test-web test-ci

install:
	go mod download
	npm --prefix apps/web ci

dev-api:
	go run ./cmd/homeatlas -public-url http://127.0.0.1:$(WEB_PORT)

dev-web:
	npm --prefix apps/web run dev -- --port $(WEB_PORT) --strictPort

build:
	npm --prefix apps/web run build
	go build -tags production -o bin/homeatlas ./cmd/homeatlas

GO_TEST_TIMEOUT ?= 20m

test: test-go test-wasi test-web

test-go:
	go test -race -timeout=$(GO_TEST_TIMEOUT) ./...
	go test -race -timeout=$(GO_TEST_TIMEOUT) ./internal/app/testdata/legacydocuments
	go vet ./...

test-wasi:
	go test -timeout=$(GO_TEST_TIMEOUT) ./internal/app -run '^TestPersistentPlugin' -count=1

# Push/PR profile: cheap units with race plus selected real security/data flows.
# Keep these explicit selectors aligned with test renames; make test remains exhaustive.
CI_IDENTITY_TESTS := ^Test(SetupLoginRotationLogoutAndRestart|SetupIsAtomicAndSingleUse|SessionExpiryAndMembershipRevocation|HTTPSAndCrossOriginProtection|LocalRecoveryRevokesSessionsAndDoesNotChangeAccess|PINSessionCannotBecomeAdministrativeAfterPermissionChange|NewPasswordLengthBoundaries)$$
CI_GUARD_TESTS := ^Test(BackupExportDoesNotQueueDeclaredPluginQueries|BackupExportAllowsReadersButFreezesMutations|BackupRestoreStillExcludesReaders|SettingsAndModuleChangesAreAtomicAndProtectedFromCSRF)$$
CI_APP_TESTS := ^Test(HTTPFoundation|StorageSettingsDefaultPermissionsCSRFAndValidation|EncryptedRestoreAuthenticatesAllBytesBeforeEitherImport|PersistentPluginExternalDocuments|PersistentPluginDocumentsAdoptsExistingSchema|PersistentPluginS3StorageSwitchBackupAndRecovery|PersistentPluginMoveOriginalsPreservesMetadataAndSharing)$$

test-ci:
	go test -race -timeout=$(GO_TEST_TIMEOUT) ./cmd/homeatlas ./plugins/documents \
		./internal/core/access ./internal/core/backup ./internal/core/collections \
		./internal/core/database ./internal/core/mailer ./internal/core/modules \
		./internal/core/objectstore ./internal/core/plugins
	go test -race -timeout=$(GO_TEST_TIMEOUT) ./internal/core/identity -run '$(CI_IDENTITY_TESTS)'
	go test -race -timeout=$(GO_TEST_TIMEOUT) ./internal/app -run '$(CI_GUARD_TESTS)'
	go test -timeout=$(GO_TEST_TIMEOUT) ./internal/app -run '$(CI_APP_TESTS)' -count=1
	go vet ./...

test-web:
	npm --prefix apps/web test
	npm --prefix apps/web run build

# Build only the external WASI package. Use a fresh PACKAGE_OUT path.
PACKAGE_OUT ?= bin/plugins/documents.hap
.PHONY: plugin-documents
plugin-documents:
	mkdir -p bin/plugins
	GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags='-s -w' -o bin/plugins/documents.wasm ./plugins/documents
	python3 scripts/package-plugin.py plugins/documents/manifest.json bin/plugins/documents.wasm "$(PACKAGE_OUT)"
