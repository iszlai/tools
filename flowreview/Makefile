BIN     := bin/flowreview
PREFIX  ?= $(HOME)/.local/bin

VERSION ?= $(shell git describe --tags --match 'flowreview-v*' --dirty 2>/dev/null | sed 's/^flowreview-//')
ifeq ($(strip $(VERSION)),)
VERSION := dev
endif

# The window shim and the tree-sitter grammars are C/Objective-C.
export CGO_ENABLED := 1

.DEFAULT_GOAL := help

help: ## show this help
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | expand -t22

build: ## build bin/flowreview
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/flowreview

test: ## run unit and end-to-end tests (no network, no browser)
	go test ./...

check: fmt vet test ## format, vet and test

fmt: ## gofmt everything
	gofmt -l -w cmd internal

vet: ## go vet
	go vet ./...

smoke: build ## drive the UI in headless Chromium (see tests/smoke.js)
	PLAYWRIGHT_BROWSERS_PATH=./pw-browsers node tests/smoke.js

install: build ## install bin/flowreview into PREFIX
	install -m 0755 $(BIN) $(PREFIX)/flowreview

release: ## build the darwin/arm64 release binary into dist/
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/flowreview_darwin_arm64 ./cmd/flowreview
	@ls -l dist

clean: ## remove build output
	rm -rf bin dist

.PHONY: help build test check fmt vet smoke install release clean
