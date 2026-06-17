VERSION := $(shell grep -oP 'Version: \K[0-9]+\.[0-9]+\.[0-9]+' README.md)
LDFLAGS := -ldflags "-X github.com/zot/ark.Version=$(VERSION)"
BUILDFLAGS := -buildvcs=false
GOTAGS :=

# Sibling project locations (adjust if needed)
FRICTIONLESS_DIR ?= ../frictionless
FRICTIONLESS_BIN := $(FRICTIONLESS_DIR)/build/frictionless

CACHE_DIR := cache
RELEASE_DIR := release

.PHONY: build install test clean cache cache-clean cache-refresh markdown-editor ark-search pdf-chunk tag-overview release release-archives

# Default: deps, cache, build+bundle
all: cache markdown-editor ark-search pdf-chunk tag-overview build

# Build markdown editor JS bundle
markdown-editor:
	@$(MAKE) -C markdown-editor build

# Build ark-search element JS bundle + CSS
ark-search:
	@$(MAKE) -C ark-search build

# Build pdf-chunk element JS bundle + pdfjs worker
pdf-chunk:
	@$(MAKE) -C pdf-chunk build

# Build tag-overview JS bundle (sidebar + <ark-ext-tags>)
tag-overview:
	@$(MAKE) -C tag-overview build

# Build Go binary and graft cached assets.
# R2971: no gollama/cmake/Vulkan recipe — the embedding engine (yzma) dlopens
# llama.cpp shared libs at runtime (provisioned via `ark embed install`), so the
# binary builds pure-Go (CGO_ENABLED=0) with no C cross-toolchain.
build:
	CGO_ENABLED=0 go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o bin/ark ./cmd/ark
	@rm -f $(CACHE_DIR)/.cached
	bin/ark bundle -o bin/ark.bundled $(CACHE_DIR)
	@touch $(CACHE_DIR)/.cached
	mv bin/ark.bundled bin/ark

# Install bundled binary to ~/.ark/ (CGO-free; llama.cpp libs are provisioned
# at runtime via `ark embed install`, not linked or bundled).
install: build
	@mkdir -p ~/.ark
	@if [ -L ~/.ark/ark ] || [ ! bin/ark -ef ~/.ark/ark ]; then \
		cp -f bin/ark ~/.ark/ark; \
	fi

# R2972: frictionless-style cross-platform release sweep. The pure-Go
# (CGO_ENABLED=0) binary cross-compiles freely now that the store (bbolt) and
# the embedding engine (yzma) carry no C deps. Each target binary gets the
# cached assets grafted via `ark bundle -src`. llama.cpp libs are NOT bundled —
# they are provisioned per host at runtime by `ark embed install`.
release: all
	@echo "Building release binaries..."
	@mkdir -p $(RELEASE_DIR)
	@# Linux AMD64
	@echo "  Building linux/amd64..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o $(RELEASE_DIR)/ark-linux-amd64 ./cmd/ark
	@bin/ark bundle -src $(RELEASE_DIR)/ark-linux-amd64 -o $(RELEASE_DIR)/ark-linux-amd64.bundled $(CACHE_DIR)
	@mv $(RELEASE_DIR)/ark-linux-amd64.bundled $(RELEASE_DIR)/ark-linux-amd64
	@# Linux ARM64
	@echo "  Building linux/arm64..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o $(RELEASE_DIR)/ark-linux-arm64 ./cmd/ark
	@bin/ark bundle -src $(RELEASE_DIR)/ark-linux-arm64 -o $(RELEASE_DIR)/ark-linux-arm64.bundled $(CACHE_DIR)
	@mv $(RELEASE_DIR)/ark-linux-arm64.bundled $(RELEASE_DIR)/ark-linux-arm64
	@# macOS AMD64
	@echo "  Building darwin/amd64..."
	@CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o $(RELEASE_DIR)/ark-darwin-amd64 ./cmd/ark
	@bin/ark bundle -src $(RELEASE_DIR)/ark-darwin-amd64 -o $(RELEASE_DIR)/ark-darwin-amd64.bundled $(CACHE_DIR)
	@mv $(RELEASE_DIR)/ark-darwin-amd64.bundled $(RELEASE_DIR)/ark-darwin-amd64
	@# macOS ARM64 (Apple Silicon)
	@echo "  Building darwin/arm64..."
	@CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o $(RELEASE_DIR)/ark-darwin-arm64 ./cmd/ark
	@bin/ark bundle -src $(RELEASE_DIR)/ark-darwin-arm64 -o $(RELEASE_DIR)/ark-darwin-arm64.bundled $(CACHE_DIR)
	@mv $(RELEASE_DIR)/ark-darwin-arm64.bundled $(RELEASE_DIR)/ark-darwin-arm64
	@# Windows AMD64
	@echo "  Building windows/amd64..."
	@CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o $(RELEASE_DIR)/ark-windows-amd64.exe ./cmd/ark
	@bin/ark bundle -src $(RELEASE_DIR)/ark-windows-amd64.exe -o $(RELEASE_DIR)/ark-windows-amd64.bundled.exe $(CACHE_DIR)
	@mv $(RELEASE_DIR)/ark-windows-amd64.bundled.exe $(RELEASE_DIR)/ark-windows-amd64.exe
	@echo "Release binaries in $(RELEASE_DIR)/"
	@ls -la $(RELEASE_DIR)/

# R2972: per-platform release archives (tar.gz for unix, zip for windows).
release-archives: release
	@echo "Creating release archives..."
	@cd $(RELEASE_DIR) && \
		for f in ark-*; do \
			if [ -f "$$f" ] && ! echo "$$f" | grep -q '\.\(tar\.gz\|zip\)$$'; then \
				if echo "$$f" | grep -q "windows"; then \
					zip -q "$${f%.exe}.zip" "$$f" && echo "  Created $${f%.exe}.zip"; \
				else \
					tar -czf "$$f.tar.gz" "$$f" && echo "  Created $$f.tar.gz"; \
				fi; \
			fi; \
		done
	@echo "Release archives created"

# Cache: extract frictionless assets, layer ark's own app on top
cache: $(CACHE_DIR)/.cached

$(FRICTIONLESS_BIN):
	@echo "Building frictionless..."
	@cd $(FRICTIONLESS_DIR); $(MAKE) build

# Source files whose edits should invalidate the cache. find/wildcard
# are evaluated at parse time, so adding a new file requires a fresh
# `make` invocation — but edits to existing files are picked up.
CACHE_SRC := \
	$(shell find apps/ark -type f 2>/dev/null) \
	$(shell find install -type f 2>/dev/null) \
	.claude/skills/ark/SKILL.md \
	.claude/skills/ui/SKILL.md \
	.claude/agents/ark-franklin.md \
	.claude/agents/ark-messenger.md \
	.claude/agents/ark-searcher.md \
	$(wildcard markdown-editor/dist/*) \
	$(wildcard ark-search/dist/*) \
	$(wildcard pdf-chunk/dist/*) \
	$(wildcard tag-overview/dist/*)

#@note: need to scrape emacs backups out of $(CACHE_DIR)/apps/ark after copy
$(CACHE_DIR)/.cached: $(FRICTIONLESS_BIN) $(CACHE_SRC)
	@echo "Extracting frictionless assets..."
	$(FRICTIONLESS_BIN) extract $(CACHE_DIR)
	@echo "Layering ark app..."
	@mkdir -p $(CACHE_DIR)/apps/ark
	cp -r apps/ark/* $(CACHE_DIR)/apps/ark/
	@echo "Layering skills, agents, and install assets..."
	@mkdir -p $(CACHE_DIR)/skills/ark $(CACHE_DIR)/skills/ui $(CACHE_DIR)/agents $(CACHE_DIR)/install
	cp .claude/skills/ark/SKILL.md $(CACHE_DIR)/skills/ark/
	cp .claude/skills/ui/SKILL.md $(CACHE_DIR)/skills/ui/
	cp .claude/agents/ark-franklin.md .claude/agents/ark-messenger.md .claude/agents/ark-searcher.md $(CACHE_DIR)/agents/
	cp -r install/* $(CACHE_DIR)/install/
	@echo "Layering markdown editor and content templates..."
	@mkdir -p $(CACHE_DIR)/html
	@if [ -d markdown-editor/dist ]; then cp markdown-editor/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d ark-search/dist ]; then cp ark-search/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d pdf-chunk/dist ]; then cp pdf-chunk/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d tag-overview/dist ]; then cp tag-overview/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d install/html ]; then cp install/html/* $(CACHE_DIR)/html/; fi
	@touch $(CACHE_DIR)/.cached
	@echo "Cached assets in $(CACHE_DIR)/"

cache-refresh: cache-clean cache

cache-clean:
	rm -rf $(CACHE_DIR)

test:
	go test $(BUILDFLAGS) $(GOTAGS) ./...

clean:
	rm -rf bin $(CACHE_DIR) $(RELEASE_DIR)
