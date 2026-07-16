.PHONY: build test clean watch

# xterm's stylesheet rides in the bundle as text and is injected on load
# (R3160), so a page needs one <script> and no stylesheet link.
ESBUILD_FLAGS := --bundle --format=esm --sourcemap --loader:.css=text

build: node_modules
	npx esbuild src/luhmann-terminal-element.ts --outfile=dist/luhmann-terminal-element.js $(ESBUILD_FLAGS)

# The pure core's rules (crc-PtyProtocol.md). Node strips the types natively —
# no runner, no dependency.
test: node_modules
	node --test src/pty-protocol.test.ts

watch: node_modules
	npx esbuild src/luhmann-terminal-element.ts --outfile=dist/luhmann-terminal-element.js $(ESBUILD_FLAGS) --watch

node_modules: package.json
	npm install
	@touch node_modules

clean:
	rm -rf dist node_modules
