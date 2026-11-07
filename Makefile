.PHONY: build test lint check

build:
	go build -o bin/plonge ./cmd/plonge

test:
	gotestsum --format pkgname -- ./...

lint:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

check: lint test
