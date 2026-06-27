.PHONY: lint test build build-wasm serve-wasm devserver run clean fmt install-tools watch

lint:
	GOOS=js GOARCH=wasm go vet ./...

test:
	go test -v ./...

build:
	go build -o dist/rift .

run:
	go run .

build-wasm:
	mkdir -p dist
	GOOS=js GOARCH=wasm go build -o dist/main.wasm .
	cp $$(go env GOROOT)/lib/wasm/wasm_exec.js dist/
	cp web/* dist/

serve-wasm: build-wasm
	go run ./devserver

devserver:
	go run ./devserver

install-tools:
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/air-verse/air@latest

fmt: install-tools
	goimports -w .

watch:
	air

clean:
	rm -rf dist

release: build-wasm
	cd dist && zip -r ../rift-$$(git rev-parse --short HEAD).zip .
