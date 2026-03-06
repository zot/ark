.PHONY: build clean

build:
	go build -buildvcs=false -o bin/ark ./cmd/ark

clean:
	rm -rf bin
