BIN     := bin/flowreview
PREFIX  ?= $(HOME)/.local/bin

VERSION ?= $(shell git describe --tags --match 'flowreview-v*' --dirty 2>/dev/null | sed 's/^flowreview-//')
ifeq ($(strip $(VERSION)),)
VERSION := dev
endif

# The window shim and the tree-sitter grammars are C/Objective-C.
export CGO_ENABLED := 1

# Set once third_party/llama/dist/ has been populated by `make fetch-llama`,
# so a plain `make build` embeds the real llama-server and `go build ./...`
# elsewhere (make check, go vet) keeps working from a fresh checkout, which
# has no dist/ and falls back to the stub provider. Recursively expanded (=,
# not :=) so `make fetch-llama release` sees dist/ appear from the first
# target before the second target's recipe evaluates this.
LLAMA_DIST := third_party/llama/dist
LLAMA_TAGS = $(if $(wildcard $(LLAMA_DIST)/llama-server),-tags llama_embed,)

.DEFAULT_GOAL := help

help: ## show this help
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | expand -t22

build: ## build bin/flowreview (embeds llama-server if third_party/llama/dist/ is populated)
	go build -trimpath $(LLAMA_TAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/flowreview

fetch-llama: ## download the pinned llama-server build into third_party/llama/dist/
	third_party/llama/fetch.sh

test: ## run unit and end-to-end tests (no network, no browser)
	go test ./...

check: fmt vet test ## format, vet and test

fmt: ## gofmt everything
	gofmt -l -w cmd internal

vet: ## go vet
	go vet ./...

smoke: build ## drive the UI in headless Chromium (see tests/smoke.js, tests/smoke-summary.js)
	PLAYWRIGHT_BROWSERS_PATH=$${PLAYWRIGHT_BROWSERS_PATH:-../taskhound/pw-browsers} node tests/smoke.js
	PLAYWRIGHT_BROWSERS_PATH=$${PLAYWRIGHT_BROWSERS_PATH:-../taskhound/pw-browsers} node tests/smoke-summary.js

eval: fetch-llama ## run the FR-27 model eval harness against the default tier models; see eval/reports/
	go run ./cmd/flowreview-eval --models qwen2.5-coder-1.5b-instruct-q4_k_m,qwen2.5-coder-3b-instruct-q4_k_m,qwen2.5-coder-7b-instruct-q4_k_m

install: ## build and install flowreview + the agent skill
	./install.sh --prefix $(PREFIX)

update: ## rebuild and reinstall over an existing install
	./install.sh --update --prefix $(PREFIX)

uninstall: ## remove flowreview and the agent skill
	./install.sh --prefix $(PREFIX) --uninstall

# darwin/arm64 only for now (M5's Mac); amd64 is a TODO (FR-36 PLAN.md note)
# once a cgo cross-compiler or an amd64 runner is wired up for the llama
# sidecar and the tree-sitter grammars.
release: fetch-llama ## build the darwin/arm64 release binary (with embedded llama-server) into dist/
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build -trimpath $(LLAMA_TAGS) -ldflags "-s -w -X main.version=$(VERSION)" -o dist/flowreview_darwin_arm64 ./cmd/flowreview
	(cd dist && shasum -a 256 flowreview_darwin_arm64 > flowreview_darwin_arm64.sha256)
	@ls -l dist

clean: ## remove build output
	rm -rf bin dist

.PHONY: help build fetch-llama test check fmt vet smoke eval install update uninstall release clean
