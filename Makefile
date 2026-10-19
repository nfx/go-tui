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

site/public/: $(BIN)/hugo $(shell find site -name "*.md")
	rm -rf site/public
	$(BIN)/hugo build --source site --buildDrafts --baseURL / --minify

$(BIN)/htmltest: | $(BIN)
	GOBIN=$(BIN) go install github.com/wjdp/htmltest@v0.17.0

lint-site: $(BIN)/htmltest site/public/
	cd site && $(BIN)/htmltest

site-preview: $(BIN)/hugo
	$(BIN)/hugo server --source site --buildDrafts --disableFastRender

examples:
	cd examples && go build ./... && go vet ./...

run-examples:
	sh examples/all.sh

configure:
	@hooks=$$(git rev-parse --git-path hooks) && \
	mkdir -p $$hooks && \
	printf '#!/bin/sh\nset -e\nmake check\n' > $$hooks/pre-commit && \
	chmod +x $$hooks/pre-commit && \
	echo "installed $$hooks/pre-commit"

godoc:
	go run golang.org/x/tools/cmd/godoc -http=localhost:6060

check: fmt lint test examples lint-site

.PHONY: check fmt lint test examples lint-site configure