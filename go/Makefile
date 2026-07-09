.PHONY: test vet build all

GOCACHE ?= $(CURDIR)/.gocache
export GOCACHE

test:
	go test ./...

vet:
	go vet ./...

build:
	go build ./cmd/symphony

all: test vet build
