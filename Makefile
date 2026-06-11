VERSION := $(shell grep -oP 'Version: \K[0-9]+\.[0-9]+\.[0-9]+' README.md)
LDFLAGS := -ldflags "-X github.com/zot/ark.Version=$(VERSION)"
BUILDFLAGS := -buildvcs=false
GOTAGS :=

# Sibling project locations (adjust if needed)
FRICTIONLESS_DIR ?= ../frictionless
FRICTIONLESS_BIN := $(FRICTIONLESS_DIR)/build/frictionless
GOLLAMA_DIR ?= ../gollama

CACHE_DIR := cache

.PHONY: build install test clean cache cache-clean cache-refresh markdown-editor ark-search pdf-chunk tag-overview

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

# Build Go binary and graft cached assets
build: gollama
	go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o bin/ark ./cmd/ark
	@rm -f $(CACHE_DIR)/.cached
	bin/ark bundle -o bin/ark.bundled $(CACHE_DIR)
	@touch $(CACHE_DIR)/.cached
	mv bin/ark.bundled bin/ark

# Install bundled binary to ~/.ark/ (statically linked, no shared libs needed)
install: build
	@mkdir -p ~/.ark
	@if [ -L ~/.ark/ark ] || [ ! bin/ark -ef ~/.ark/ark ]; then \
		cp -f bin/ark ~/.ark/ark; \
	fi

# Build gollama with Vulkan GPU acceleration and GGML_NATIVE=OFF.
# GGML_NATIVE=OFF avoids SIGILL on Zen 2 (Steam Deck) — -march=native
# enables instructions the CPU reports but can't execute.
# Vulkan offloads compute to GPU: 45ms/chunk vs 235ms/chunk on CPU.
# Only runtime dependency: libvulkan.so.1 (standard on GPU-capable systems).
gollama: $(GOLLAMA_DIR)/libbinding.a

$(GOLLAMA_DIR)/libbinding.a:
	@echo "Building gollama (Vulkan + GGML_NATIVE=OFF)..."
	cd $(GOLLAMA_DIR) && rm -rf build && mkdir build && \
		/usr/bin/cmake -S llama.cpp -B build \
			-DGGML_NATIVE=OFF \
			-DGGML_VULKAN=ON \
			-DBUILD_SHARED_LIBS=OFF \
			-DLLAMA_BUILD_EXAMPLES=OFF \
			-DLLAMA_BUILD_TESTS=OFF \
			-DLLAMA_BUILD_SERVER=OFF && \
		/usr/bin/cmake --build build --config Release -j$$(nproc) && \
		make libbinding.a
	@echo "gollama build complete"

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
	rm -rf bin $(CACHE_DIR)
