.PHONY: build test test-race lint clean install

BINARY_NAME=bin/zgo
VERSION=2.0.0
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "devel")
DATE=$(shell date -u +%Y-%m-%d)

LDFLAGS=-s -w -X main.Version=$(VERSION) -X main.GitCommit=$(COMMIT) -X main.BuildDate=$(DATE)

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/zgo

test:
	go test -v ./...

test-race:
	go test -v -race ./...

doctor: build
	./$(BINARY_NAME) doctor

clean:
	rm -rf bin/ dist/

install: build
	cp $(BINARY_NAME) $(HOME)/go/bin/zgo
