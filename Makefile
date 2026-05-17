BINARY  := nh-helper
PKG     := ./cmd/nh-helper
BINDIR  := bin
LDFLAGS := -s -w
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Per-arch build matrix used to produce the release artefacts.
# darwin-amd64 + darwin-arm64 are fused into a single universal
# binary via lipo; linux and windows ship amd64 only. Final outputs:
#   bin/nh-helper-darwin       (universal: amd64 + arm64)
#   bin/nh-helper-linux        (amd64)
#   bin/nh-helper-windows.exe  (amd64)
PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	windows/amd64

.PHONY: build build-all test vet tidy clean release help

help:  ## Show this help
	@awk 'BEGIN{FS=":.*## "}/^[a-zA-Z_-]+:.*## /{printf "  \033[1m%-12s\033[0m %s\n",$$1,$$2}' $(MAKEFILE_LIST)

build:  ## Build for the host platform → bin/nh-helper(.exe)
	@mkdir -p $(BINDIR)
	go build -ldflags "$(LDFLAGS) -X main.version=$(VERSION)" -o $(BINDIR)/$(BINARY)$(if $(filter windows,$(shell go env GOOS)),.exe,) $(PKG)
	@echo "→ $(BINDIR)/$(BINARY)$(if $(filter windows,$(shell go env GOOS)),.exe,)"

build-all:  ## Cross-compile release artefacts: darwin (universal), linux, windows
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
	@command -v lipo >/dev/null 2>&1 || { \
		echo "✗ lipo not found — needed to fuse darwin amd64+arm64 into a universal binary"; exit 1; }
	@echo "  · fusing darwin amd64+arm64 → $(BINDIR)/$(BINARY)-darwin"
	@lipo -create -output $(BINDIR)/$(BINARY)-darwin \
		$(BINDIR)/$(BINARY)-darwin-amd64 $(BINDIR)/$(BINARY)-darwin-arm64
	@rm -f $(BINDIR)/$(BINARY)-darwin-amd64 $(BINDIR)/$(BINARY)-darwin-arm64
	@mv $(BINDIR)/$(BINARY)-linux-amd64       $(BINDIR)/$(BINARY)-linux
	@mv $(BINDIR)/$(BINARY)-windows-amd64.exe $(BINDIR)/$(BINARY)-windows.exe
	@echo "✓ release artefacts: $(BINARY)-darwin, $(BINARY)-linux, $(BINARY)-windows.exe (under $(BINDIR)/)"

test:  ## Run the test suite
	go test ./...

vet:  ## go vet
	go vet ./...

tidy:  ## go mod tidy
	go mod tidy

clean:  ## Remove built binaries and generated runtime files
	rm -rf $(BINDIR) log
	rm -f nh-helper nh-helper.exe nh-helper-手册.pdf
	rm -f nh-helper.raw.log nh-helper.translate.log  # legacy root paths

release: clean tidy vet test build-all  ## Full pre-release pipeline
	@echo "✓ release artefacts in $(BINDIR)/"
