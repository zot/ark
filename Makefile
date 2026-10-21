VERSION := $(shell grep -oP 'Version: \K[0-9]+\.[0-9]+\.[0-9]+' README.md)
LDFLAGS := -ldflags "-X github.com/zot/ark.Version=$(VERSION)"
BUILDFLAGS := -buildvcs=false
GOTAGS :=

# Sibling project locations (adjust if needed)
FRICTIONLESS_DIR ?= ../frictionless
FRICTIONLESS_BIN := $(FRICTIONLESS_DIR)/build/frictionless

CACHE_DIR := cache
RELEASE_DIR := release
# R3544: each version builds into its own directory; nothing else under release/ is touched.
RELEASE_VERSION_DIR := $(RELEASE_DIR)/$(VERSION)
RELEASE_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build install test clean cache cache-clean cache-refresh markdown-editor ark-search pdf-chunk tag-overview luhmann-terminal release release-sums release-archives

# Default: deps, cache, build+bundle
all: cache markdown-editor ark-search pdf-chunk tag-overview luhmann-terminal build

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

# Build <luhmann-terminal> JS bundle (xterm.js + its CSS, inlined — R3160)
luhmann-terminal:
	@$(MAKE) -C luhmann-terminal build

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
	@echo "Building release binaries into $(RELEASE_VERSION_DIR)..."
	@rm -rf $(RELEASE_VERSION_DIR) && mkdir -p $(RELEASE_VERSION_DIR)
	@# R3545: each binary is named by `ark _release-asset`, the function `ark update`
	@# uses to choose its download, so the release and the updater cannot disagree.
	@set -e; for p in $(RELEASE_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		name=$$(bin/ark _release-asset --terse $$os $$arch); \
		echo "  Building $$os/$$arch -> $$name"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(BUILDFLAGS) $(GOTAGS) $(LDFLAGS) -o $(RELEASE_VERSION_DIR)/$$name.raw ./cmd/ark; \
		bin/ark bundle -src $(RELEASE_VERSION_DIR)/$$name.raw -o $(RELEASE_VERSION_DIR)/$$name $(CACHE_DIR); \
		rm $(RELEASE_VERSION_DIR)/$$name.raw; \
	done
	@$(MAKE) --no-print-directory release-sums
	@ls -la $(RELEASE_VERSION_DIR)/

# R3546: SHA256SUMS lists every file in the version's release directory, in
# sha256sum format (what `ark update` verifies against).
release-sums:
	@cd $(RELEASE_VERSION_DIR) && rm -f SHA256SUMS && sha256sum -- * > SHA256SUMS.tmp && mv SHA256SUMS.tmp SHA256SUMS
	@echo "Wrote $(RELEASE_VERSION_DIR)/SHA256SUMS"

# R2972: per-platform release archives (tar.gz for unix, zip for windows) beside
# the binaries, for manual download; SHA256SUMS is rewritten to cover them.
release-archives: release
	@echo "Creating release archives..."
	@cd $(RELEASE_VERSION_DIR) && \
		for f in ark-*; do \
			if [ -f "$$f" ] && ! echo "$$f" | grep -q '\.\(tar\.gz\|zip\)$$'; then \
				if echo "$$f" | grep -q "windows"; then \
					zip -q "$${f%.exe}.zip" "$$f" && echo "  Created $${f%.exe}.zip"; \
				else \
					tar -czf "$$f.tar.gz" "$$f" && echo "  Created $$f.tar.gz"; \
				fi; \
			fi; \
		done
	@$(MAKE) --no-print-directory release-sums
	@echo "Release archives created"

# Cache: extract frictionless assets, layer ark's own app on top
cache: $(CACHE_DIR)/.cached

$(FRICTIONLESS_BIN):
	@echo "Building frictionless..."
	@cd $(FRICTIONLESS_DIR); $(MAKE) build

# Skills and agents bundled into the binary. Single source of truth for both
# the cache-invalidation prerequisites (CACHE_SRC) and the copy step in the
# $(CACHE_DIR)/.cached recipe below, so the two lists cannot drift apart. Named
# here; the SKILL.md / .md source paths derive from the names. The ui-* skills
# are not listed: `frictionless extract` supplies them, whole directories.
CACHE_SKILLS := ark ark-search ark-usage bloodhound luhmann recall
CACHE_AGENTS := ark-franklin ark-franklin-opus ark-messenger ark-recall-agent ark-searcher
CACHE_SKILL_SRC := $(foreach s,$(CACHE_SKILLS),.claude/skills/$(s)/SKILL.md)
CACHE_AGENT_SRC := $(foreach a,$(CACHE_AGENTS),.claude/agents/$(a).md)

# Source files whose edits should invalidate the cache. find/wildcard
# are evaluated at parse time, so adding a new file requires a fresh
# `make` invocation — but edits to existing files are picked up.
CACHE_SRC := \
	$(shell find apps/ark -type f 2>/dev/null) \
	$(shell find install -type f 2>/dev/null) \
	$(CACHE_SKILL_SRC) \
	$(CACHE_AGENT_SRC) \
	$(wildcard markdown-editor/dist/*) \
	$(wildcard ark-search/dist/*) \
	$(wildcard pdf-chunk/dist/*) \
	$(wildcard tag-overview/dist/*) \
	$(wildcard luhmann-terminal/dist/*)

#@note: need to scrape emacs backups out of $(CACHE_DIR)/apps/ark after copy
$(CACHE_DIR)/.cached: $(FRICTIONLESS_BIN) $(CACHE_SRC)
	@echo "Extracting frictionless assets..."
	$(FRICTIONLESS_BIN) extract $(CACHE_DIR)
	@echo "Layering ark app..."
	@mkdir -p $(CACHE_DIR)/apps/ark
	cp -r apps/ark/* $(CACHE_DIR)/apps/ark/
	@rm -rf $(CACHE_DIR)/apps/mcp
	@mkdir -p $(CACHE_DIR)/apps/mcp
	cp -r apps/mcp/* $(CACHE_DIR)/apps/mcp/
	@echo "Layering skills, agents, and install assets..."
	@mkdir -p $(CACHE_DIR)/agents $(CACHE_DIR)/install
	@for s in $(CACHE_SKILLS); do \
		mkdir -p $(CACHE_DIR)/skills/$$s; \
		cp .claude/skills/$$s/SKILL.md $(CACHE_DIR)/skills/$$s/; \
	done
	cp $(CACHE_AGENT_SRC) $(CACHE_DIR)/agents/
	cp -r install/* $(CACHE_DIR)/install/
	@echo "Layering markdown editor and content templates..."
	@mkdir -p $(CACHE_DIR)/html
	@if [ -d markdown-editor/dist ]; then cp markdown-editor/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d ark-search/dist ]; then cp ark-search/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d pdf-chunk/dist ]; then cp pdf-chunk/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d tag-overview/dist ]; then cp tag-overview/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d luhmann-terminal/dist ]; then cp luhmann-terminal/dist/* $(CACHE_DIR)/html/; fi
	@if [ -d install/html ]; then cp install/html/* $(CACHE_DIR)/html/; fi
	@# R3520: the bundle ships no symlinks; setup activates apps itself
	@# (crc-CLI.md, specs/bundle-assets.md). ark bundle refuses any left.
	@find $(CACHE_DIR) -type l -delete
	@touch $(CACHE_DIR)/.cached
	@echo "Cached assets in $(CACHE_DIR)/"

cache-refresh: cache-clean cache

cache-clean:
	rm -rf $(CACHE_DIR)

test:
	go test $(BUILDFLAGS) $(GOTAGS) ./...
	@$(MAKE) -C luhmann-terminal test

clean:
	rm -rf bin $(CACHE_DIR) $(RELEASE_DIR)
