.PHONY: build clean watch

build: node_modules
	npx esbuild src/tag-overview.ts --bundle --outfile=dist/tag-overview.js --format=esm --sourcemap

watch: node_modules
	npx esbuild src/tag-overview.ts --bundle --outfile=dist/tag-overview.js --format=esm --sourcemap --watch

node_modules: package.json
	npm install
	@touch node_modules

clean:
	rm -rf dist node_modules
