BIN     := bin/th
PREFIX  ?= $(HOME)/.local/bin
PORT    ?= 8787
VERSION ?= dev

# Dependencies are vendored, so build and test never need the network or a
# working module proxy.
export GOFLAGS := -mod=vendor

.DEFAULT_GOAL := help

help: ## show this help
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | expand -t22

build: ## build bin/th
	go build -trimpath -o $(BIN) .

test: ## run the unit and end-to-end tests
	go test ./...

check: fmt vet test ## format, vet and test

fmt: ## gofmt everything
	gofmt -l -w *.go

vet: ## go vet
	go vet ./...

install: ## build and install th + the agent skill
	./install.sh --prefix $(PREFIX)

uninstall: ## remove th and the agent skill
	./install.sh --prefix $(PREFIX) --uninstall

ui: build ## serve the kanban board for the board in this directory
	$(BIN) ui --port $(PORT) --open

release: ## cross-compile every target into dist/
	mkdir -p dist && rm -f dist/th_*
	for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do \
	  os=$${target%/*}; arch=$${target#*/}; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath \
	    -ldflags "-s -w -X main.version=$(VERSION)" \
	    -o dist/th_$${os}_$${arch} . || exit 1; \
	done
	@ls -l dist

vendor: ## refresh the vendored dependencies (needs network)
	GOFLAGS= go mod tidy && GOFLAGS= go mod vendor

clean: ## remove build output
	rm -f bin/th dist/th_*

.PHONY: help build test check fmt vet install uninstall ui release vendor clean
