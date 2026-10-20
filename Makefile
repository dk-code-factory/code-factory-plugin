.PHONY: build test check ui package verify plugin connection-test connection-sdk-test
ui:
	cd web && npm ci && npm run build
build: ui
	python3 scripts/package_plugin.py --embed
	mkdir -p bin
	go build -trimpath -o bin/code-factory ./cmd/code-factory
check:
	go vet ./...
	cd web && npm run check
test:
	go build -o bin/fake-codex ./internal/testutil/fakecodex
	go test -race ./...
	cd web && npm test
	python3 scripts/test_packaging.py
	python3 scripts/test_live_connection_test.py
	python3 scripts/test_connection_probe.py
	node --test scripts/test_browser_lab.mjs
package: build
	python3 scripts/package.py
verify:
	./scripts/verify.sh

plugin:
	python3 scripts/package_plugin.py --embed
	python3 scripts/test_packaging.py

# Opt-in tests against the installed gateway; no ChatGPT login or model calls.
connection-test:
	python3 scripts/test_live_connection.py --full

connection-sdk-test:
	node scripts/test_mcp_sdk.mjs
