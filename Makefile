.PHONY: build test lint fmt check clean

BIN := bin/skills-manager

build:
	go build -o $(BIN) ./cmd/skills-manager

test:
	go test ./...

lint:
	go vet ./...

fmt:
	gofmt -w cmd internal

check: fmt lint test build

clean:
	rm -rf bin dist coverage.out
