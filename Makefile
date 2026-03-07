VERSION := $(shell grep -oP 'Version: \K[0-9]+\.[0-9]+\.[0-9]+' README.md)
LDFLAGS := -ldflags "-X ark.Version=$(VERSION)"
BUILDFLAGS := -buildvcs=false

.PHONY: build install test clean

build:
	go build $(BUILDFLAGS) $(LDFLAGS) ./...

install: build
	go build $(BUILDFLAGS) $(LDFLAGS) -o ~/.ark/ark ./cmd/ark

test:
	go test $(BUILDFLAGS) ./...

clean:
	rm -f ~/.ark/ark
