.DEFAULT_GOAL := ui
# Expand after all selected files have been read. The native caller graph keeps
# its own recipes/defaults; existing managed worker boundaries still recurse.
verification-goals = $(if $(MAKECMDGOALS),$(MAKECMDGOALS),$(.DEFAULT_GOAL))
# A caller alias may reach timed recursion later. Retain stdin now, but replay it
# only at that repository-managed boundary; native caller recipes are never replayed.
# Make 4.3 does not export parsed Make variables into $(shell), whose command
# parser also removes literal LF. Escape backslashes/LF before shell transport;
# imported environment values retain their literal dollars rather than expanding.
define verification-newline


endef
verification-temporary-parent = $(if $(findstring environment,$(origin TMPDIR)),$(value TMPDIR),$(TMPDIR))
verification-encoded-parent = $(subst $(verification-newline),\n,$(subst \,\\,$(verification-temporary-parent)))
verification-capture-stdin = $(eval override export CODE_FACTORY_MAKE_STDIN := $(shell python3 scripts/verification_run.py --capture-make-stdin $(shell echo $$PPID) '$(subst ','"'"',$(MAKEFILE_LIST))'))
# Managed entrypoints compose Git ceilings from Make's actual exported environment.
verification-owner-fields = CODE_FACTORY_VERIFY_RUN CODE_FACTORY_VERIFY_ROOT CODE_FACTORY_VERIFY_TOKEN CODE_FACTORY_VERIFY_TMPDIR TMPDIR
# Read fields after eval parses syntax. A live owner's directory-FD alias keeps
# pathname bytes out of shell-output normalization and Make assignment parsing.
verification-startup = $(verification-capture-stdin)$(eval verification-owner := $(shell python3 scripts/verification_run.py --acquire-make-owner $(shell echo $$PPID) '$(subst ','"'"',$(verification-encoded-parent))'))$(if $(verification-owner),,$(error Cannot acquire verification ownership))$(eval verification-fields := $(shell python3 scripts/verification_run.py --make-owner-fields '$(verification-owner)'))$(if $(verification-fields),,$(error Cannot export verification ownership))$(foreach field,$(verification-owner-fields),$(eval override export $(field) := $$(file <$(verification-fields)/$(field))))
Makefile: private override .EXTRA_PREREQS = $(verification-startup)
# Normalize inherited/explicit -k at the verification boundary. All preparation
# and phases stay in one child graph. When a caller replaces MAKEFLAGS, retain
# GNU Make's parsed options (including -n) and safely quote command-line overrides.
# Escape stored dollars before reassigning an already-expanded simple variable.
verification-options = $(if $(filter command,$(origin MAKEFLAGS)),$(MFLAGS))
verification-overrides = $(if $(filter command,$(origin MAKEFLAGS)),\
  $(foreach _verification_arg,$(filter-out MAKEFLAGS,$(.VARIABLES)),\
    $(if $(filter command,$(origin $(_verification_arg))),\
      '$(subst ','"'"',$(_verification_arg)$(if $(filter simple,$(flavor $(_verification_arg))),:=$(subst $$,$$$$,$(value $(_verification_arg))),=$(value $(_verification_arg))))')))

# Successful native dry runs create no automatic timing file. Early failure or
# interruption before native mode is known retains an unknown partial, even
# for a dry attempt.
# GNU Make serializes no-argument short flags first, followed by argument options.
# Include paths and output modes may contain n without requesting a dry run.
# Prefer native MAKEFLAGS unless the caller replaced that carrier explicitly.
verification-native-options = $(if $(filter command,$(origin MAKEFLAGS)),$(MFLAGS),$(MAKEFLAGS))
verification-short-options = $(filter-out --% -C% -f% -I% -j% -l% -O% -W% -o%,$(firstword $(verification-native-options)))
# Source identity starts in the worker; comparison comes from the actual check.
verification-runner = $(if $(findstring n,$(verification-short-options)),,env CODE_FACTORY_TIMING_WORKER_CONTEXT=1 python3 scripts/verification_timing.py verify --)

# Native recursive forwarding can carry the caller's original TMPDIR assignment.
# Reuse authenticated same-checkout state as raw values before any helper export;
# cross-checkout calls still select their own caller-supplied temporary parent.
ifeq ($(value CODE_FACTORY_VERIFY_ROOT),$(CURDIR))
ifneq ($(value CODE_FACTORY_VERIFY_TMPDIR),)
override export TMPDIR := $(value CODE_FACTORY_VERIFY_TMPDIR)
override export CODE_FACTORY_VERIFY_RUN := $(value CODE_FACTORY_VERIFY_RUN)
override export CODE_FACTORY_VERIFY_ROOT := $(value CODE_FACTORY_VERIFY_ROOT)
override export CODE_FACTORY_VERIFY_TOKEN := $(value CODE_FACTORY_VERIFY_TOKEN)
override export CODE_FACTORY_VERIFY_TMPDIR := $(value CODE_FACTORY_VERIFY_TMPDIR)
endif
endif

.PHONY: build test test-api-performance test-performance test-real-provider check ui package verify plugin connection-test connection-sdk-test flows-validate flows-check specs-validate specs-check specs-ci specs-report specs-gaps ci npm-deps plugin-embed fake-provider hosted-prerequisites python-deps browser-deps hosted-verify hosted-verify-bootstrap verify-bootstrap test-installer smoke
.PHONY: local-verification-prepare verify-steps verify-bootstrap-steps hosted-verify-steps hosted-verify-bootstrap-steps verification-context
.DEFAULT_GOAL := ui
# Ordinary verification is deterministic even with inherited or command-line opt-ins.
override export CODE_FACTORY_REAL_CODEX := 0
FLOW_BASE ?=
FLOW_ARGS ?= --base $(if $(FLOW_BASE),$(FLOW_BASE),HEAD)
# Share preparation only inside this Make graph. Independent invocations always
# prepare current inputs, including changed tooling and missing outputs.
# Serialize phases even when the caller supplies -j or inherited MAKEFLAGS.
.NOTPARALLEL: verify-steps verify-bootstrap-steps hosted-verify-steps hosted-verify-bootstrap-steps hosted-prerequisites build test test-api-performance test-performance
npm-deps:
	cd web && python3 ../scripts/verification_timing.py command npm-install -- npm ci
ui: npm-deps
	cd web && python3 ../scripts/verification_timing.py command frontend -- npm run build
plugin-embed:
	python3 scripts/verification_timing.py command embed -- python3 scripts/package_plugin.py --embed
fake-provider:
	mkdir -p bin
	python3 scripts/verification_timing.py command fake-provider -- go build -o bin/fake-codex ./internal/testutil/fakecodex
build: ui plugin-embed
	mkdir -p bin
	python3 scripts/verification_timing.py command build -- go build -trimpath -o bin/code-factory ./cmd/code-factory
check: npm-deps
	python3 scripts/verification_timing.py command specifications-validate -- python3 scripts/specs.py validate
	python3 scripts/verification_timing.py command static -- go vet ./...
	cd web && python3 ../scripts/verification_timing.py command frontend-check -- npm run check
test: ui plugin-embed fake-provider
	python3 scripts/verification_timing.py command correctness -- go test -race -count=1 ./...
	cd web && python3 ../scripts/verification_timing.py command frontend-tests -- npm test
	python3 scripts/verification_timing.py command tooling-packaging -- python3 scripts/test_packaging.py
	python3 scripts/verification_timing.py command tooling-live-connection-test -- python3 scripts/test_live_connection_test.py
	python3 scripts/verification_timing.py command tooling-connection-probe -- python3 scripts/test_connection_probe.py
	python3 scripts/verification_timing.py command browser-lab -- node --test scripts/test_browser_lab.mjs
	python3 scripts/verification_timing.py command browser-control -- node --test scripts/test_browser_control.mjs
	python3 scripts/verification_timing.py command fixture-listener -- node --test scripts/test_fixture_listener.mjs
	python3 scripts/verification_timing.py command tooling-ci -- python3 scripts/test_ci.py
	python3 scripts/verification_timing.py command tooling-browser-benchmark -- python3 scripts/test_browser_benchmark.py
	python3 scripts/verification_timing.py command tooling-user-flows -- python3 scripts/test_user_flows.py
	python3 scripts/verification_timing.py command specification-tests -- python3 -m unittest discover -s scripts -p 'test_specs*.py'
test-api-performance: ui plugin-embed fake-provider
	python3 scripts/verification_timing.py command api-performance -- go test -p 1 -count=1 -run APILatency ./internal/transport ./internal/gateway ./internal/setup ./internal/factory ./internal/httpbudget -args -api-performance

test-performance: test-api-performance ui plugin-embed fake-provider
	cd web && python3 ../scripts/verification_timing.py command performance -- npm run test:performance
package: build
	python3 scripts/verification_timing.py command packaging -- python3 scripts/package.py
verify verify-bootstrap hosted-verify hosted-verify-bootstrap:
	+@python3 scripts/verification_run.py --make-parent $(shell echo $$PPID) -- $(verification-runner) $(MAKE) $(verification-options) --no-keep-going $@-steps $(verification-overrides)

verification-context:
	python3 scripts/verification_timing.py worker-start

# Explicit combined-local preparation; ordinary verification retains its graph.
local-verification-prepare: build fake-provider package
	cd web && python3 ../scripts/verification_timing.py command browser-optimize -- node node_modules/vite/bin/vite.js optimize

verify-steps: verification-context specs-check build check test test-performance package test-installer smoke

# Only scripts/ci.py selects this path for the pinned reviewed public root.
verify-bootstrap-steps: verification-context specs-validate build check test test-performance package test-installer smoke

test-installer:
	python3 scripts/verification_timing.py command installer -- python3 scripts/test_installer.py

smoke:
	python3 scripts/verification_timing.py command smoke -- python3 scripts/smoke.py

python-deps:
	python3 scripts/verification_timing.py command python-install -- python3 -m pip install -r scripts/requirements-dev.txt

browser-deps: npm-deps
	cd web && python3 ../scripts/verification_timing.py command browser-install -- npx playwright install --with-deps chromium

hosted-prerequisites: python-deps browser-deps

hosted-verify-steps: verification-context hosted-prerequisites verify-steps

hosted-verify-bootstrap-steps: verification-context hosted-prerequisites verify-bootstrap-steps

flows-validate: specs-validate

flows-check: specs-check

specs-validate:
	python3 scripts/verification_timing.py command specifications-validate -- python3 scripts/specs.py validate

specs-check:
	python3 scripts/verification_timing.py command specifications -- python3 scripts/specs.py check $(FLOW_ARGS)

specs-report:
	python3 scripts/specs.py report $(FLOW_ARGS)

specs-gaps:
	python3 scripts/specs.py gaps $(FLOW_ARGS)

specs-ci: specs-validate
	python3 scripts/verification_timing.py command tooling-ci -- python3 scripts/test_ci.py
	python3 scripts/verification_timing.py command tooling-browser-benchmark -- python3 scripts/test_browser_benchmark.py
	python3 scripts/verification_timing.py command tooling-user-flows -- python3 scripts/test_user_flows.py
	python3 scripts/verification_timing.py command specification-tests -- python3 -m unittest discover -s scripts -p 'test_specs*.py'
	+@python3 scripts/verification_run.py --make-parent $(shell echo $$PPID) -- $(MAKE) specs-check

ci:
	@test -n "$(FLOW_BASE)" || { echo 'Set FLOW_BASE to the branch or commit being compared'; exit 1; }
	+@python3 scripts/verification_run.py --make-parent $(shell echo $$PPID) -- $(verification-runner) $(MAKE) $(verification-options) --no-keep-going verify-steps $(verification-overrides) FLOW_ARGS='--base $(FLOW_BASE) --merge-base'

# Explicit installed-provider acceptance; may make real model calls.
test-real-provider:
	python3 scripts/verification_run.py -- env CODE_FACTORY_REAL_CODEX=1 go test -count=1 ./internal/codex ./internal/app

plugin: plugin-embed
	python3 scripts/verification_timing.py command tooling-packaging -- python3 scripts/test_packaging.py

# Opt-in tests against the installed gateway; no ChatGPT login or model calls.
connection-test:
	python3 scripts/test_live_connection.py --full

connection-sdk-test:
	node scripts/test_mcp_sdk.mjs
