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
	go build -o bin/goyt-inspect ./cmd/goyt-inspect
	go build -o bin/goyt-hls ./cmd/goyt-hls

run:
	go run ./cmd/goyt

check: vet test-race build
