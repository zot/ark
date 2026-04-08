VERSION := $(shell grep -oP 'Version: \K[0-9]+\.[0-9]+\.[0-9]+' README.md)
LDFLAGS := -ldflags "-X ark.Version=$(VERSION)"
BUILDFLAGS := -buildvcs=false
GOTAGS := -tags vulkan
# Link Vulkan; rpath finds gollama shared libs at ~/.ark/lib at runtime
CGO_LDFLAGS := -lvulkan -Wl,-rpath,$(HOME)/.ark/lib

# Sibling project locations (adjust if needed)
FRICTIONLESS_DIR ?= ../frictionless
FRICTIONLESS_BIN := $(FRICTIONLESS_DIR)/build/frictionless
GOLLAMA_DIR ?= ../gollama

CACHE_DIR := cache

.PHONY: build install test clean cache cache-clean cache-refresh markdown-editor

# Default: deps, cache, build+bundle
all: cache markdown-editor build

# Build markdown editor JS bundle
markdown-editor:
	@$(MAKE) -C markdown-editor build

# Build Go binary and graft cached assets
build: gollama
	CGO_LDFLAGS="$(CGO_LDFLAGS)" go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o bin/ark ./cmd/ark
	@rm -f $(CACHE_DIR)/.cached
	bin/ark bundle -o bin/ark.bundled $(CACHE_DIR)
	@touch $(CACHE_DIR)/.cached
	mv bin/ark.bundled bin/ark

# Install shared libs to ~/.ark/lib/ (gollama + Vulkan)
install-libs:
	@mkdir -p ~/.ark/lib
	cp -a $(GOLLAMA_DIR)/prebuilt/linux_amd64/*.so* ~/.ark/lib/

# Install bundled binary to ~/.ark/
install: build install-libs
	@mkdir -p ~/.ark
	@if [ -L ~/.ark/ark ] || [ ! bin/ark -ef ~/.ark/ark ]; then \
		cp -f bin/ark ~/.ark/ark; \
	fi

# Build gollama with Vulkan support (needed for embedding on Zen 2 / Steam Deck)
# The go workspace resolves gollama from GOLLAMA_DIR.
gollama: $(GOLLAMA_DIR)/libbinding.a

$(GOLLAMA_DIR)/libbinding.a:
	@echo "Building gollama with Vulkan..."
	cd $(GOLLAMA_DIR) && rm -rf build && mkdir build && \
		/usr/bin/cmake -S llama.cpp -B build \
			-DGGML_VULKAN=ON \
			-DBUILD_SHARED_LIBS=OFF \
			-DLLAMA_BUILD_EXAMPLES=OFF \
			-DLLAMA_BUILD_TESTS=OFF \
			-DLLAMA_BUILD_SERVER=OFF && \
		/usr/bin/cmake --build build --config Release -j$$(nproc) && \
		make libbinding.a
	@echo "gollama Vulkan build complete"

# Cache: extract frictionless assets, layer ark's own app on top
cache: $(CACHE_DIR)/.cached

$(FRICTIONLESS_BIN):
	@echo "Building frictionless..."
	@cd $(FRICTIONLESS_DIR); $(MAKE) build

#@note: need to scrape emacs backups out of $(CACHE_DIR)/apps/ark after copy
$(CACHE_DIR)/.cached: $(FRICTIONLESS_BIN)
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
