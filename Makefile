BINARY  := nh-helper
PKG     := ./cmd/nh-helper
BINDIR  := bin
LDFLAGS := -s -w
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# All supported OS / arch pairs. Each target writes
# bin/<BINARY>-<os>-<arch>[.exe].
PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/arm64

.PHONY: build build-all test vet tidy clean release help

help:  ## Show this help
	@awk 'BEGIN{FS=":.*## "}/^[a-zA-Z_-]+:.*## /{printf "  \033[1m%-12s\033[0m %s\n",$$1,$$2}' $(MAKEFILE_LIST)

build:  ## Build for the host platform → bin/nh-helper(.exe)
	@mkdir -p $(BINDIR)
	go build -ldflags "$(LDFLAGS) -X main.version=$(VERSION)" -o $(BINDIR)/$(BINARY)$(if $(filter windows,$(shell go env GOOS)),.exe,) $(PKG)
	@echo "→ $(BINDIR)/$(BINARY)$(if $(filter windows,$(shell go env GOOS)),.exe,)"

build-all:  ## Cross-compile for darwin / linux / windows × amd64 / arm64
	@mkdir -p $(BINDIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=$(BINDIR)/$(BINARY)-$$os-$$arch$$ext; \
		echo "  · building $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS) -X main.version=$(VERSION)" -o $$out $(PKG) \
			|| exit 1; \
	done
	@echo "✓ all platforms built under $(BINDIR)/"

test:  ## Run the test suite
	go test ./...

vet:  ## go vet
	go vet ./...

tidy:  ## go mod tidy
	go mod tidy

clean:  ## Remove built binaries and generated runtime files
	rm -rf $(BINDIR)
	rm -f nh-helper $(BINARY)-*.log nh-helper-手册.pdf

release: clean tidy vet test build-all  ## Full pre-release pipeline
	@echo "✓ release artefacts in $(BINDIR)/"
