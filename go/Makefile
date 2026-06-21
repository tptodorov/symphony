.PHONY: test vet build all

test:
	go test ./...

vet:
	go vet ./...

build:
	go build ./cmd/symphony

all: test vet build
