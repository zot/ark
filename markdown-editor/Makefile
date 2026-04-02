.PHONY: build clean watch

build: node_modules
	npx esbuild src/index.ts --bundle --outfile=dist/ark-markdown-editor.js --format=esm --sourcemap

watch: node_modules
	npx esbuild src/index.ts --bundle --outfile=dist/ark-markdown-editor.js --format=esm --sourcemap --watch

node_modules: package.json
	npm install
	@touch node_modules

clean:
	rm -rf dist node_modules
