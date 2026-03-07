VERSION := $(shell grep -oP 'Version: \K[0-9]+\.[0-9]+\.[0-9]+' README.md)
LDFLAGS := -ldflags "-X ark.Version=$(VERSION)"
BUILDFLAGS := -buildvcs=false

# Sibling project locations (adjust if needed)
FRICTIONLESS_DIR ?= ../frictionless
FRICTIONLESS_BIN := $(FRICTIONLESS_DIR)/build/frictionless

CACHE_DIR := cache

.PHONY: build install test clean cache cache-clean cache-refresh

# Default: deps, cache, build+bundle
all: cache build

# Build Go binary and graft cached assets
build:
	go build $(BUILDFLAGS) $(LDFLAGS) -o bin/ark ./cmd/ark
	@rm -f $(CACHE_DIR)/.cached
	bin/ark bundle -o bin/ark.bundled $(CACHE_DIR)
	@touch $(CACHE_DIR)/.cached
	mv bin/ark.bundled bin/ark

# Install bundled binary to ~/.ark/
install: build
	@mkdir -p ~/.ark
	@if [ -L ~/.ark/ark ] || [ ! bin/ark -ef ~/.ark/ark ]; then \
		cp -f bin/ark ~/.ark/ark; \
	fi

# Cache: extract frictionless assets, layer ark's own app on top
cache: $(CACHE_DIR)/.cached

$(FRICTIONLESS_BIN):
	@echo "Building frictionless..."
	@cd $(FRICTIONLESS_DIR); $(MAKE) build

$(CACHE_DIR)/.cached: $(FRICTIONLESS_BIN)
	@echo "Extracting frictionless assets..."
	$(FRICTIONLESS_BIN) extract $(CACHE_DIR)
	@echo "Layering ark app..."
	@mkdir -p $(CACHE_DIR)/apps/ark
	cp -r apps/ark/* $(CACHE_DIR)/apps/ark/
	@touch $(CACHE_DIR)/.cached
	@echo "Cached assets in $(CACHE_DIR)/"

cache-refresh: cache-clean cache

cache-clean:
	rm -rf $(CACHE_DIR)

test:
	go test $(BUILDFLAGS) ./...

clean:
	rm -rf bin $(CACHE_DIR)
