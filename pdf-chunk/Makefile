.PHONY: build clean watch

build: node_modules
	npx esbuild src/pdf-chunk-element.ts --bundle --outfile=dist/pdf-chunk-element.js --format=esm --sourcemap
	npx esbuild node_modules/pdfjs-dist/build/pdf.worker.mjs --bundle --outfile=dist/pdf-chunk-element.worker.js --format=esm --sourcemap

watch: node_modules
	npx esbuild src/pdf-chunk-element.ts --bundle --outfile=dist/pdf-chunk-element.js --format=esm --sourcemap --watch

node_modules: package.json
	npm install
	@touch node_modules

clean:
	rm -rf dist node_modules
