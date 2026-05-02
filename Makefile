SHELL := /bin/bash

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/selfcloud/selfcloud/internal/version.Version=$(VERSION) \
	-X github.com/selfcloud/selfcloud/internal/version.Commit=$(COMMIT) \
	-X github.com/selfcloud/selfcloud/internal/version.Date=$(DATE)

GO          ?= go
BUN         ?= bun
BIN_DIR     := bin
DASHBOARD   := web

PLATFORMS := linux/amd64 linux/arm64

.PHONY: all
all: web build

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: web
web:
	cd $(DASHBOARD) && $(BUN) install --frozen-lockfile && $(BUN) run build
	mkdir -p internal/api/web_dist
	rm -rf internal/api/web_dist/*
	cp -r $(DASHBOARD)/dist/* internal/api/web_dist/

.PHONY: build
build:
	mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/selfcloud ./cmd/selfcloud

.PHONY: build-all
build-all: web
	mkdir -p $(BIN_DIR)
	@for plat in $(PLATFORMS); do \
		os=$${plat%/*}; arch=$${plat#*/}; \
		echo "==> $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
			-o $(BIN_DIR)/selfcloud-$$os-$$arch ./cmd/selfcloud; \
	done

.PHONY: terraform-provider
terraform-provider:
	cd terraform-provider-selfcloud && \
		$(GO) build -trimpath -ldflags '-s -w' -o ../$(BIN_DIR)/terraform-provider-selfcloud .

.PHONY: test
test:
	$(GO) test -race -count=1 ./...

.PHONY: lint
lint:
	$(GO) vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "golangci-lint not installed, skipping"; fi

.PHONY: dev
dev:
	$(GO) run ./cmd/selfcloud server --data-dir ./data --dev

.PHONY: dev-web
dev-web:
	cd $(DASHBOARD) && $(BUN) run dev

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) internal/api/web_dist $(DASHBOARD)/dist $(DASHBOARD)/node_modules data
