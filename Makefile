VERSION := $(shell grep -oP 'Version: \K[0-9]+\.[0-9]+\.[0-9]+' README.md)
LDFLAGS := -ldflags "-X ark.Version=$(VERSION)"
BUILDFLAGS := -buildvcs=false

.PHONY: build install test clean

build:
	go build $(BUILDFLAGS) $(LDFLAGS) -o bin/ark ./cmd/ark

install: build
	cp bin/ark ~/.ark/ark

test:
	go test $(BUILDFLAGS) ./...

clean:
	rm -f ~/.ark/ark
