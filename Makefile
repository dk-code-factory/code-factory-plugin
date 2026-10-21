.PHONY: build test test-real-provider check ui package verify plugin connection-test connection-sdk-test flows-validate flows-check specs-validate specs-check specs-ci specs-report specs-gaps ci
# Ordinary verification is deterministic even with inherited or command-line opt-ins.
override export CODE_FACTORY_REAL_CODEX := 0
FLOW_BASE ?=
FLOW_ARGS ?= --base $(if $(FLOW_BASE),$(FLOW_BASE),HEAD)
ui:
	cd web && npm ci && npm run build
build: ui
	python3 scripts/package_plugin.py --embed
	mkdir -p bin
	go build -trimpath -o bin/code-factory ./cmd/code-factory
check:
	python3 scripts/specs.py validate
	go vet ./...
	cd web && npm run check
test:
	go build -o bin/fake-codex ./internal/testutil/fakecodex
	go test -race -count=1 ./...
	cd web && npm test
	python3 scripts/test_packaging.py
	python3 scripts/test_live_connection_test.py
	python3 scripts/test_connection_probe.py
	node --test scripts/test_browser_lab.mjs
	node --test scripts/test_browser_control.mjs
	python3 scripts/test_ci.py
	python3 scripts/test_user_flows.py
	python3 -m unittest discover -s scripts -p 'test_specs*.py'
package: build
	python3 scripts/package.py
verify:
	./scripts/verify.sh

flows-validate: specs-validate

flows-check: specs-check

specs-validate:
	python3 scripts/specs.py validate

specs-check:
	python3 scripts/specs.py check $(FLOW_ARGS)

specs-report:
	python3 scripts/specs.py report $(FLOW_ARGS)

specs-gaps:
	python3 scripts/specs.py gaps $(FLOW_ARGS)

specs-ci: specs-validate
	python3 scripts/test_ci.py
	python3 scripts/test_user_flows.py
	python3 -m unittest discover -s scripts -p 'test_specs*.py'
	$(MAKE) specs-check

ci:
	@test -n "$(FLOW_BASE)" || { echo 'Set FLOW_BASE to the branch or commit being compared'; exit 1; }
	$(MAKE) verify FLOW_ARGS='--base $(FLOW_BASE) --merge-base'

# Explicit installed-provider acceptance; may make real model calls.
test-real-provider:
	CODE_FACTORY_REAL_CODEX=1 go test -count=1 ./internal/codex ./internal/app

plugin:
	python3 scripts/package_plugin.py --embed
	python3 scripts/test_packaging.py

# Opt-in tests against the installed gateway; no ChatGPT login or model calls.
connection-test:
	python3 scripts/test_live_connection.py --full

connection-sdk-test:
	node scripts/test_mcp_sdk.mjs
