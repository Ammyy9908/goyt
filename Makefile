.PHONY: fmt vet test test-race build run check

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

build:
	mkdir -p bin
	go build -o bin/goyt ./cmd/goyt

run:
	go run ./cmd/goyt

check: vet test-race build
