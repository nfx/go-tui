BIN := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))/.bin

$(BIN):
	@mkdir -p $@

$(BIN)/golangci-lint: | $(BIN)
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2

lint: $(BIN)/golangci-lint .golangci.json $(wildcard *.go)
	$(BIN)/golangci-lint run --fix

fmt: $(BIN)/golangci-lint .golangci.json $(wildcard *.go)
	$(BIN)/golangci-lint fmt

test: $(wildcard *.go)
	go test ./...

$(BIN)/hugo: | $(BIN)
	GOBIN=$(BIN) go install github.com/gohugoio/hugo@v0.165.0

site/examples: $(shell find examples -name "*.go" -not -name "*_test.go")
	rm -rf $@
	rsync -a --prune-empty-dirs --include='*/' --include='main.go' --exclude='*' examples/ $@/

site/public/: $(BIN)/hugo site/examples $(shell find site -name "*.md")
	rm -rf site/public
	$(BIN)/hugo build --source site --buildDrafts --baseURL / --minify

$(BIN)/htmltest: | $(BIN)
	GOBIN=$(BIN) go install github.com/wjdp/htmltest@v0.17.0

lint-site: $(BIN)/htmltest site/public/
	cd site && $(BIN)/htmltest

site-preview: $(BIN)/hugo site/examples
	$(BIN)/hugo server --source site --buildDrafts --disableFastRender

examples:
	cd examples && go build ./... && go vet ./...

run-examples:
	sh examples/run.sh

configure:
	@hooks=$$(git rev-parse --git-path hooks) && \
	mkdir -p $$hooks && \
	printf '#!/bin/sh\nset -e\nmake check\n' > $$hooks/pre-commit && \
	chmod +x $$hooks/pre-commit && \
	echo "installed $$hooks/pre-commit"

godoc:
	go run golang.org/x/tools/cmd/godoc -http=localhost:6060

cross-build:
	GOOS=windows go vet ./...
	GOOS=windows go test -exec=true -run=^$$ ./...
	GOOS=linux go vet ./...
	GOOS=linux go test -exec=true -run=^$$ ./...

check: fmt lint test cross-build examples lint-site

$(BIN)/asciinema: | $(BIN)
	@curl -fsSL -o $@.tmp https://github.com/asciinema/asciinema/releases/download/v3.2.1/asciinema-aarch64-apple-darwin
	@echo "1f0c76da7855601df93e5dccdf69b7c683b81beff1411e38b3802de1f5fc7a1c  $@.tmp" | shasum -a 256 -c - || { rm -f $@.tmp; exit 1; }
	@chmod +x $@.tmp && mv $@.tmp $@

$(BIN)/agg: | $(BIN)
	@curl -fsSL -o $@.tmp https://github.com/asciinema/agg/releases/download/v1.9.0/agg-aarch64-apple-darwin
	@echo "742b2b6230529b72f310acb835e9479496000f2eabc97b0993cabe1d7fe70171  $@.tmp" | shasum -a 256 -c - || { rm -f $@.tmp; exit 1; }
	@chmod +x $@.tmp && mv $@.tmp $@

.PHONY: check fmt lint test cross-build examples lint-site configure