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

.PHONY: build build-all pack test vet tidy clean release help

PACK_NAME := $(BINARY)-pack-$(shell date +%Y%m%d-%H%M%S)

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

pack: build-all  ## Bundle binaries + current config + db into a portable zip
	@command -v zip >/dev/null 2>&1 || { echo "✗ zip not found"; exit 1; }
	@rm -rf $(BINDIR)/$(PACK_NAME)
	@mkdir -p $(BINDIR)/$(PACK_NAME)/$(BINARY)/bin
	@cp $(BINDIR)/$(BINARY)-darwin       $(BINDIR)/$(PACK_NAME)/$(BINARY)/bin/
	@cp $(BINDIR)/$(BINARY)-linux        $(BINDIR)/$(PACK_NAME)/$(BINARY)/bin/
	@cp $(BINDIR)/$(BINARY)-windows.exe  $(BINDIR)/$(PACK_NAME)/$(BINARY)/bin/
	@chmod +x $(BINDIR)/$(PACK_NAME)/$(BINARY)/bin/*
	@if [ -f config.json ]; then cp config.json $(BINDIR)/$(PACK_NAME)/$(BINARY)/; \
		else echo "  ⚠ no config.json yet — pack will prompt on first launch"; fi
	@if [ -f db/nh-helper.db ]; then \
		mkdir -p $(BINDIR)/$(PACK_NAME)/$(BINARY)/db && \
		cp db/nh-helper.db $(BINDIR)/$(PACK_NAME)/$(BINARY)/db/; \
		else echo "  ⚠ no db/nh-helper.db — pack will start with an empty cache"; fi
	@if [ -f nh-helper-手册.pdf ]; then cp nh-helper-手册.pdf $(BINDIR)/$(PACK_NAME)/$(BINARY)/; fi
	@printf '%s\n' \
		'# nh-helper portable pack' \
		'' \
		'解压后的目录结构：' \
		'  $(BINARY)/' \
		'    bin/nh-helper-darwin       macOS (universal)' \
		'    bin/nh-helper-linux        Linux amd64' \
		'    bin/nh-helper-windows.exe  Windows amd64' \
		'    config.json                凭据 (SSH + OpenRouter key)' \
		'    db/nh-helper.db            翻译缓存 + 词条库' \
		'    nh-helper-手册.pdf         中文速查' \
		'' \
		'## 怎么用' \
		'' \
		'```' \
		'cd $(BINARY)' \
		'./bin/nh-helper-darwin     # 或 ./bin/nh-helper-linux' \
		'```' \
		'' \
		'二进制会自动探测父目录 (`..`)，找到 config.json / db/ 直接复用，' \
		'不会重新弹首次设置向导。' \
		'' \
		'## ⚠ 安全提示' \
		'' \
		'config.json 里有：' \
		'  - alt.org 的 SSH 凭据（一般是公共账号 nethack/空密码）' \
		'  - OpenRouter API key' \
		'不要把这个 zip 发上公开渠道或丢失给陌生人。' \
		> $(BINDIR)/$(PACK_NAME)/$(BINARY)/README.txt
	@cd $(BINDIR)/$(PACK_NAME) && zip -r -q ../$(PACK_NAME).zip $(BINARY)
	@rm -rf $(BINDIR)/$(PACK_NAME)
	@printf '\n'
	@ls -lh $(BINDIR)/$(PACK_NAME).zip
	@echo "✓ packed → $(BINDIR)/$(PACK_NAME).zip"
	@echo "  ⚠ contains config.json (SSH credentials + OpenRouter key) — do not share publicly"

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
