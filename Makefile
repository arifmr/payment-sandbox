# Backend test targets.
#
# `make help` lists everything. The coverage targets exist because a bare
# `go test ./... -cover` prints every package and leaves the reader to eyeball which ones
# clear a bar — fine once, tedious in a loop.

# Coverage bar, in percent. Override per invocation:  make test-cover THRESHOLD=80
THRESHOLD ?= 90

# -count=1 disables the test result cache. Without it a "passing" run can be entirely
# cached output, which is exactly wrong when the point is to measure something.
GOTEST ?= go test -count=1

COVERPROFILE ?= coverage.out

# Extracts "<pct> <package>" from `go test -cover` output.
#
# Written as awk rather than sed because the fields are tab-separated and BSD sed (macOS)
# does not interpret \t inside a bracket expression — it silently treats it as the letter
# 't' and truncates every package name at the 't' in "payment".
#
# Packages with no test files still print a coverage line (0.0%), so they fall out of the
# >= THRESHOLD filter on their own; no special case needed.
define pkgs_at_or_above
$(GOTEST) ./... -cover 2>/dev/null | awk -v min=$(THRESHOLD) '\
/coverage:/ { \
  pkg=""; pct=""; \
  for (i=1; i<=NF; i++) { \
    if ($$i ~ /^github\.com\//) pkg=$$i; \
    if ($$i == "coverage:") { pct=$$(i+1); sub(/%/, "", pct) } \
  } \
  if (pkg != "" && pct+0 >= min) print pkg \
}'
endef

define pkgs_below
$(GOTEST) ./... -cover 2>/dev/null | awk -v min=$(THRESHOLD) '\
/coverage:/ { \
  pkg=""; pct=""; \
  for (i=1; i<=NF; i++) { \
    if ($$i ~ /^github\.com\//) pkg=$$i; \
    if ($$i == "coverage:") { pct=$$(i+1); sub(/%/, "", pct) } \
  } \
  if (pkg != "" && pct+0 < min) printf "%6.1f%%  %s\n", pct, pkg \
}' | sort -rn
endef

# ── performance (k6) ──────────────────────────────────────────────────────────
# BASE_URL points at the API directly. Use http://localhost:3000 to measure through
# nginx instead, which is what a browser actually experiences.
BASE_URL  ?= http://localhost:8080
VUS       ?= 10
DURATION  ?= 30s
SLO_MS    ?= 300
K6_SCRIPT ?= test/k6/load.js

.DEFAULT_GOAL := help
.PHONY: help test test-cover cover cover-list cover-below cover-func cover-gaps cover-html cover-total verify perf perf-smoke perf-soak

help: ## Show this help
	@echo "Backend test targets (THRESHOLD=$(THRESHOLD))"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "  Override the bar:  make test-cover THRESHOLD=80"

test: ## Run every unit test (no coverage)
	$(GOTEST) ./...

test-cover: ## Run ONLY packages whose coverage is >= THRESHOLD
	@pkgs=$$($(call pkgs_at_or_above)); \
	if [ -z "$$pkgs" ]; then \
	  echo "No package reaches $(THRESHOLD)% coverage."; exit 1; \
	fi; \
	echo "Packages at or above $(THRESHOLD)%:"; echo "$$pkgs" | sed 's|^|  |'; echo; \
	echo "$$pkgs" | xargs $(GOTEST) -cover

cover: ## Coverage for every package
	$(GOTEST) ./... -cover

cover-list: ## List packages >= THRESHOLD, with their percentage
	@$(GOTEST) ./... -cover 2>/dev/null | awk -v min=$(THRESHOLD) '\
	/coverage:/ { \
	  pkg=""; pct=""; \
	  for (i=1; i<=NF; i++) { \
	    if ($$i ~ /^github\.com\//) pkg=$$i; \
	    if ($$i == "coverage:") { pct=$$(i+1); sub(/%/, "", pct) } \
	  } \
	  if (pkg != "" && pct+0 >= min) printf "%6.1f%%  %s\n", pct, pkg \
	}' | sort -rn

cover-below: ## List packages UNDER THRESHOLD — usually the more useful list
	@$(call pkgs_below)

cover-total: ## Single total coverage number across the module
	@$(GOTEST) ./... -coverprofile=$(COVERPROFILE) >/dev/null 2>&1 || true
	@go tool cover -func=$(COVERPROFILE) | tail -1

cover-func: ## Per-function coverage, in the terminal (~200 lines — pipe to less)
	@$(GOTEST) ./... -coverprofile=$(COVERPROFILE) >/dev/null 2>&1 || true
	@go tool cover -func=$(COVERPROFILE)

# 0.0% rows are dropped because they are almost always a whole untested package
# (cmd/api, pkg/logger) or a build-tagged one — noise that buries the real gaps.
# What is left is code that IS exercised but has untaken branches, which is where an
# extra assertion actually buys something.
cover-gaps: ## Only partially covered functions — the actionable list
	@$(GOTEST) ./... -coverprofile=$(COVERPROFILE) >/dev/null 2>&1 || true
	@go tool cover -func=$(COVERPROFILE) \
	  | awk '$$NF != "100.0%" && $$NF != "0.0%"' \
	  | sort -t$$'\t' -k3 -n \
	  || echo "Every exercised function is at 100%."

cover-html: ## Open the line-by-line coverage report in a browser
	$(GOTEST) ./... -coverprofile=$(COVERPROFILE)
	go tool cover -html=$(COVERPROFILE)

perf: ## Load test with k6 — prints p(90)/p(95)/p(99) per endpoint (needs the stack running)
	@command -v k6 >/dev/null || { echo "k6 not installed:  brew install k6"; exit 1; }
	@curl -sf $(BASE_URL)/readyz >/dev/null || { \
	  echo "$(BASE_URL) is not ready. Start it first:  docker compose up -d"; exit 1; }
	k6 run \
	  -e BASE_URL=$(BASE_URL) \
	  -e VUS=$(VUS) \
	  -e DURATION=$(DURATION) \
	  -e SLO_MS=$(SLO_MS) \
	  $(K6_SCRIPT)

perf-smoke: ## Quick 1-VU sanity run — checks the script works before spending minutes on it
	@$(MAKE) --no-print-directory perf VUS=1 DURATION=10s

perf-soak: ## Longer run at steady load, for latency drift the short run cannot show
	@$(MAKE) --no-print-directory perf VUS=$(VUS) DURATION=5m

verify: ## Format check, vet (incl. build-tagged suites), then the full test run
	@test -z "$$(gofmt -l cmd internal test)" || { echo "gofmt needed:"; gofmt -l cmd internal test; exit 1; }
	go vet ./...
	go vet -tags=e2e ./test/e2e/
	go vet -tags=integration ./internal/repository/
	$(GOTEST) ./... -race
