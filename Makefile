BINARY := tuicode
VERSION := $(shell grep -m1 'const Version' internal/ui/app.go | cut -d'"' -f2)
PLATFORM := $(shell uname -s | tr A-Z a-z)_$(shell uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/')

.PHONY: build run test vet lint clean install

build:
	go build -trimpath -ldflags "-s -w" -o dist/$(PLATFORM)/bin/$(BINARY) ./cmd/tuicode

run:
	go run ./cmd/tuicode

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

install: build
	install -Dm755 dist/$(PLATFORM)/bin/$(BINARY) $(HOME)/.local/bin/$(BINARY)

clean:
	rm -rf dist
