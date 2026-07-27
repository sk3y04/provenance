.PHONY: test vet lint build clean

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

build:
	go build -o provenance ./cmd/provenance/

clean:
	rm -f provenance

all: vet lint test build
