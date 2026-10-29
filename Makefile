WEB_PORT ?= 5173

.PHONY: install dev-api dev-web build test

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

test:
	go test -race ./...
	go test -race ./internal/app/testdata/legacydocuments
	go test ./internal/app -run '^TestPersistentPlugin' -count=1
	go vet ./...
	npm --prefix apps/web test
	npm --prefix apps/web run build

# Build only the external WASI package. Use a fresh PACKAGE_OUT path.
PACKAGE_OUT ?= bin/plugins/documents.hap
.PHONY: plugin-documents
plugin-documents:
	mkdir -p bin/plugins
	GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags='-s -w' -o bin/plugins/documents.wasm ./plugins/documents
	python3 scripts/package-plugin.py plugins/documents/manifest.json bin/plugins/documents.wasm "$(PACKAGE_OUT)"
